package main

import (
	"context"
	"strings"
	"time"

	"github.com/2fa0/snapover/internal/history"
)

type searchResult struct {
	entries []history.Entry
	err     error
}

type searchRequest struct {
	ctx    context.Context
	query  string
	result chan searchResult
}

// Each view owns one worker, so canceled searches cannot pile up readers.
// UI state is only read or changed on the main thread; workers use channels.
type historySearch struct {
	query   string
	results []history.Entry
	valid   bool
	err     error
	cancel  context.CancelFunc
	pending chan searchResult
	jobs    chan searchRequest
}

func (s *historySearch) reset() {
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel, s.pending = nil, nil
	s.query, s.results, s.valid, s.err = "", nil, false, nil
}

func (s *historySearch) filter(root context.Context, store *history.Store, entries []history.Entry, query string) []history.Entry {
	q := strings.TrimSpace(query)
	if q == "" {
		s.reset()
		return entries
	}
	if s.query == q {
		if s.pending != nil {
			select {
			case result := <-s.pending:
				s.results, s.err, s.valid, s.pending = result.entries, result.err, true, nil
			default:
			}
		}
		if s.valid || s.pending != nil {
			return s.results
		}
	}
	s.reset()
	s.query = q
	needsDisk := false
	for _, entry := range entries {
		if entry.TextFile != "" {
			needsDisk = true
			break
		}
	}
	if !needsDisk || store == nil {
		for _, entry := range entries {
			if matchesEntry(entry, q) {
				s.results = append(s.results, entry)
			}
		}
		s.valid = true
		return s.results
	}
	if root == nil {
		root = context.Background()
	}
	if s.jobs == nil {
		s.jobs = make(chan searchRequest, 1)
		go runHistorySearch(root, store, s.jobs)
	}
	ctx, cancel := context.WithCancel(root)
	s.cancel, s.pending = cancel, make(chan searchResult, 1)
	request := searchRequest{ctx: ctx, query: q, result: s.pending}
	select {
	case s.jobs <- request:
	default:
		select {
		case <-s.jobs:
		default:
		}
		s.jobs <- request
	}
	return nil
}

func runHistorySearch(root context.Context, store *history.Store, jobs <-chan searchRequest) {
	for {
		select {
		case <-root.Done():
			return
		case request := <-jobs:
			// Wait briefly for typing to settle before touching disk.
			timer := time.NewTimer(120 * time.Millisecond)
			select {
			case <-request.ctx.Done():
				timer.Stop()
				continue
			case <-timer.C:
			}
			entries, err := store.Search(request.ctx, request.query)
			if request.ctx.Err() == nil {
				request.result <- searchResult{entries: entries, err: err}
			}
		}
	}
}

func (s *historySearch) emptyMessage() string {
	if s.pending != nil {
		return "正在搜索…"
	}
	if s.err != nil {
		return "搜索失败：" + s.err.Error()
	}
	return "没有匹配的记录。"
}
