package sync

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/2fa0/snapover/internal/clip"
	"github.com/2fa0/snapover/internal/identity"
	"github.com/2fa0/snapover/internal/wire"
	"github.com/oklog/ulid/v2"
	"github.com/zeebo/blake3"
)

const (
	pingEvery     = 15 * time.Second
	readLimit     = 45 * time.Second
	queueSize     = 8
	queueBytes    = clip.MaxImageBytes
	clipboardWait = 60 * time.Second
)

// imageAssembleWait is independent of connection heartbeats.
var imageAssembleWait = 30 * time.Second

// Session is one live peer connection after Hello has been accepted.
type Session struct {
	conn      *tls.Conn
	peerID    string
	sessionID string
	weDialed  bool
	log       *slog.Logger
	onItem    func(clip.Item) error
	out       chan queuedItem

	mu                       sync.Mutex
	writeMu                  sync.Mutex
	closed                   bool
	queuedBytes              int
	lastRecv                 time.Time
	done                     chan struct{}
	once                     sync.Once
	fileSupported            bool
	receiveDir               func() string
	onFile                   func(Notice)
	fileSendMu               sync.Mutex
	fileResultMu             sync.Mutex
	fileSendID               string
	fileResults              chan wire.FileResult
	responses                chan response
	clipboardResultSupported bool
	clipboardSendMu          sync.Mutex
	clipboardResultMu        sync.Mutex
	clipboardSendID          string
	clipboardResults         chan wire.Ack
	onClipboardResult        func(wire.Ack)
	clipboardPending         map[string]time.Time
	clipboardWake            chan struct{}
	admit                    func(queuedItem) (bool, string)
	inbound                  chan inboundJob
	inboundBytes             int
}

type inboundJob struct {
	n   int
	run func()
}

type queuedItem struct {
	clip.Item
	id        string
	automatic bool
	explicit  bool
}

type response struct {
	typ  byte
	body any
}

func newSession(conn *tls.Conn, peerID string, weDialed bool, log *slog.Logger, onItem func(clip.Item) error) *Session {
	return &Session{
		conn:          conn,
		peerID:        peerID,
		sessionID:     ulid.Make().String(),
		clipboardWake: make(chan struct{}, 1),
		weDialed:      weDialed,
		log:           log,
		onItem:        onItem,
		out:           make(chan queuedItem, queueSize),
		inbound:       make(chan inboundJob, queueSize),
		responses:     make(chan response, 16),
		lastRecv:      time.Now(),
		done:          make(chan struct{}),
	}
}

func queuedSize(item clip.Item) int { return len(item.PNG) + len(item.Text) }

func (s *Session) enqueue(item clip.Item) bool {
	return s.enqueueCopy(item, "", true)
}

func (s *Session) enqueueID(item clip.Item, id string) bool {
	return s.enqueueCopy(item, id, false)
}

func (s *Session) enqueueCopy(item clip.Item, id string, automatic bool) bool {
	return s.enqueueClipboard(item, id, automatic, false)
}

func (s *Session) enqueueClipboard(item clip.Item, id string, automatic, explicit bool) bool {
	// The peer needs the original bytes, not our local thumbnail.
	item.Thumbnail = nil
	item.Preview = nil
	s.mu.Lock()
	defer s.mu.Unlock()
	n := queuedSize(item)
	if s.closed || n > queueBytes-s.queuedBytes {
		return false
	}
	automatic = automatic && s.clipboardResultSupported
	if automatic {
		if len(s.clipboardPending) >= queueSize+1 {
			return false
		}
		if id == "" {
			id = ulid.Make().String()
		}
	}
	select {
	case s.out <- queuedItem{Item: item, id: id, automatic: automatic, explicit: explicit}:
		s.queuedBytes += n
		if automatic {
			if s.clipboardPending == nil {
				s.clipboardPending = make(map[string]time.Time)
			}
			s.clipboardPending[id] = time.Now().Add(clipboardWait)
			s.wakeClipboard()
		}
		return true
	default:
		return false
	}
}

// cancelUnsentClipboard drops clipboard payloads that have not been written
// yet. keepExplicit leaves a copy-and-sync item queued. Already transmitted
// bytes cannot be recalled.
func (s *Session) cancelUnsentClipboard(keepExplicit bool, reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	var dropped, kept []queuedItem
	for {
		select {
		case item := <-s.out:
			s.queuedBytes -= queuedSize(item.Item)
			if keepExplicit && item.explicit {
				kept = append(kept, item)
				continue
			}
			delete(s.clipboardPending, item.id)
			dropped = append(dropped, item)
		default:
			for _, item := range kept {
				s.queuedBytes += queuedSize(item.Item)
				select {
				case s.out <- item:
				default:
					s.queuedBytes -= queuedSize(item.Item)
					delete(s.clipboardPending, item.id)
					dropped = append(dropped, item)
				}
			}
			s.mu.Unlock()
			for _, item := range dropped {
				s.reportClipboardCancel(item, reason)
			}
			return
		}
	}
}

func (s *Session) admitClipboard(item queuedItem) (bool, string) {
	s.mu.Lock()
	fn := s.admit
	s.mu.Unlock()
	if fn == nil {
		return true, ""
	}
	return fn(item)
}

func (s *Session) reportClipboardCancel(item queuedItem, reason string) {
	if item.id == "" || s.onClipboardResult == nil {
		return
	}
	s.onClipboardResult(wire.Ack{ItemID: item.id, State: "error", Message: reason})
}

func (s *Session) forgetClipboardPending(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	delete(s.clipboardPending, id)
	s.mu.Unlock()
	s.wakeClipboard()
}

func (s *Session) releaseQueued(item queuedItem) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.queuedBytes -= queuedSize(item.Item)
	return true
}

func (s *Session) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		// Drop queued payloads immediately, even if an active write is blocked.
		for len(s.out) > 0 {
			select {
			case <-s.out:
			default:
			}
		}
		s.queuedBytes = 0
		s.mu.Unlock()
		close(s.done)
		// Session shutdown also runs while the engine lock is held. Close the
		// transport directly so TLS close-notify cannot wait on an idle peer.
		_ = s.conn.NetConn().Close()
	})
}

func (s *Session) run() {
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		s.readLoop()
	}()
	go func() {
		defer wg.Done()
		s.processLoop()
	}()
	go func() {
		defer wg.Done()
		s.writeLoop()
	}()
	go func() {
		defer wg.Done()
		s.watchClipboard()
	}()
	wg.Wait()
}

func (s *Session) readLoop() {
	defer s.Close()
	var asm *assembler
	var incoming *fileReceive
	defer func() {
		if incoming != nil {
			incoming.file.Abort()
			s.fileNotice(incoming, "error", "", "连接已断开，未完成的文件已清理，请重新发送")
		}
	}()
	for {
		typ, payload, err := wire.ReadFrame(s.conn)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
				s.log.Debug("读连接结束", "peer", s.peerID, "err", err)
			}
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.lastRecv = time.Now()
		s.mu.Unlock()
		if incoming != nil && time.Since(incoming.updated) > fileWait {
			s.rejectFile(incoming.id, "接收超时，请重新发送")
			s.fileNotice(incoming, "error", "", "接收超时，请重新发送")
			incoming.file.Abort()
			incoming = nil
		}
		switch typ {
		case wire.TypeFileBegin, wire.TypeFileChunk, wire.TypeFileEnd, wire.TypeFileCancel:
			incoming = s.receiveFile(typ, payload, incoming)
		case wire.TypeFileResult:
			var result wire.FileResult
			if wire.Unmarshal(payload, &result) == nil {
				s.fileResultMu.Lock()
				if result.ItemID == s.fileSendID && s.fileResults != nil {
					select {
					case s.fileResults <- result:
					default:
					}
				}
				s.fileResultMu.Unlock()
			}
		case wire.TypePing:
			s.respond(wire.TypePong, nil)
		case wire.TypePong:
		case wire.TypeAck:
			var result wire.Ack
			if wire.Unmarshal(payload, &result) == nil {
				s.completeClipboard(result)
				s.clipboardResultMu.Lock()
				if result.ItemID == s.clipboardSendID && s.clipboardResults != nil {
					select {
					case s.clipboardResults <- result:
					default:
					}
				}
				s.clipboardResultMu.Unlock()
			}
		case wire.TypeError:
			var msg wire.Error
			_ = wire.Unmarshal(payload, &msg)
			s.log.Warn("对端报错", "peer", s.peerID, "msg", msg.Msg)
			return
		case wire.TypeText:
			item, err := textFromWire(payload)
			if err != nil {
				s.log.Warn("丢弃文本", "err", err)
				continue
			}
			id := itemIDOf(payload)
			if !s.queueInbound(len(item.Text), func() { s.deliverItem(id, item) }) {
				s.respond(wire.TypeAck, wire.Ack{ItemID: id, State: "error", Message: "处理队列已满"})
			}
		case wire.TypeImageBegin:
			var begin wire.ImageBegin
			if err := wire.Unmarshal(payload, &begin); err != nil {
				s.log.Warn("图片头无效", "err", err)
				continue
			}
			if asm != nil {
				asm.stop()
			}
			asm, err = newAssembler(begin)
			if err != nil {
				s.log.Warn("图片头超出限制", "err", err)
				continue
			}
			itemID := begin.ItemID
			asm.arm(func() {
				s.log.Warn("图片组装超时", "item", itemID)
				s.respond(wire.TypeAck, wire.Ack{ItemID: itemID, State: "error", Message: "图片接收超时"})
			})
		case wire.TypeImageChunk:
			if asm == nil || asm.failedNow() {
				asm.stop()
				asm = nil
				continue
			}
			var chunk wire.ImageChunk
			if err := wire.Unmarshal(payload, &chunk); err != nil {
				asm.stop()
				asm = nil
				continue
			}
			if err := asm.add(chunk); err != nil {
				s.log.Warn("图片分片无效", "err", err)
				asm.stop()
				asm = nil
			}
		case wire.TypeImageEnd:
			if asm == nil || asm.failedNow() {
				asm.stop()
				asm = nil
				continue
			}
			var end wire.ImageEnd
			if err := wire.Unmarshal(payload, &end); err != nil || end.ItemID != asm.begin.ItemID {
				asm.stop()
				asm = nil
				continue
			}
			asm.stop()
			pending := asm
			asm = nil
			id := end.ItemID
			n := pending.buffered()
			if !s.queueInbound(n, func() {
				item, err := pending.finish()
				if err != nil {
					s.log.Warn("丢弃图片", "err", err)
					s.respond(wire.TypeAck, wire.Ack{ItemID: id, State: "error", Message: err.Error()})
					return
				}
				s.deliverItem(id, item)
			}) {
				s.respond(wire.TypeAck, wire.Ack{ItemID: id, State: "error", Message: "处理队列已满"})
			}
		default:
			s.log.Debug("忽略未知帧", "type", typ)
		}
	}
}

func (s *Session) processLoop() {
	for {
		select {
		case <-s.done:
			s.drainInbound()
			return
		case job := <-s.inbound:
			if job.run != nil {
				job.run()
			}
			s.releaseInbound(job.n)
		}
	}
}

func (s *Session) queueInbound(n int, fn func()) bool {
	if s == nil || s.inbound == nil {
		fn()
		return true
	}
	if n < 0 {
		n = 0
	}
	s.mu.Lock()
	if s.closed || n > queueBytes-s.inboundBytes {
		s.mu.Unlock()
		return false
	}
	s.inboundBytes += n
	s.mu.Unlock()
	select {
	case s.inbound <- inboundJob{n: n, run: fn}:
		return true
	default:
		s.releaseInbound(n)
		return false
	}
}

func (s *Session) releaseInbound(n int) {
	s.mu.Lock()
	s.inboundBytes -= n
	if s.inboundBytes < 0 {
		s.inboundBytes = 0
	}
	s.mu.Unlock()
}

func (s *Session) drainInbound() {
	for {
		select {
		case <-s.inbound:
		default:
			return
		}
	}
}

func (s *Session) writeLoop() {
	defer s.Close()
	tick := time.NewTicker(pingEvery)
	defer tick.Stop()
	for {
		select {
		case <-s.done:
			return
		case r := <-s.responses:
			if err := s.write(r.typ, r.body); err != nil {
				return
			}
		case item := <-s.out:
			if !s.releaseQueued(item) {
				return
			}
			if ok, msg := s.admitClipboard(item); !ok {
				s.forgetClipboardPending(item.id)
				s.reportClipboardCancel(item, msg)
				continue
			}
			if !s.clipboardSendable(item, time.Now()) {
				continue
			}
			if err := s.sendItem(item.Item, item.id); err != nil {
				s.log.Warn("发送失败", "err", err)
				return
			}
		case <-tick.C:
			s.mu.Lock()
			idle := time.Since(s.lastRecv)
			s.mu.Unlock()
			if idle > readLimit {
				s.log.Warn("对端超时", "peer", s.peerID, "idle", idle.Round(time.Second))
				return
			}
			if err := s.write(wire.TypePing, nil); err != nil {
				return
			}
		}
	}
}

// Readers must keep consuming frames while the other side writes. Queue
// replies so simultaneous transfers cannot stall both read loops on writes.
func (s *Session) respond(typ byte, body any) {
	select {
	case s.responses <- response{typ: typ, body: body}:
	case <-s.done:
	default:
		s.Close()
	}
}

func (s *Session) deliverItem(id string, item clip.Item) {
	result := wire.Ack{ItemID: id, State: "complete"}
	if err := s.onItem(item); err != nil {
		result.State, result.Message = "error", err.Error()
		if errors.Is(err, errHistoryOnly) {
			result.State = "history"
		}
	}
	s.respond(wire.TypeAck, result)
}

func (s *Session) sendItem(item clip.Item, id string) error {
	now := time.Now().UnixMilli()
	if id == "" {
		id = ulid.Make().String()
	}
	switch item.Kind {
	case clip.KindText:
		return s.write(wire.TypeText, wire.Text{
			ItemID:    id,
			Hash:      item.Hash[:],
			Text:      item.Text,
			CreatedAt: now,
			SourceApp: clip.CleanSource(item.SourceApp),
		})
	case clip.KindImage:
		chunks := split(item.PNG, wire.ChunkSize)
		if err := s.write(wire.TypeImageBegin, wire.ImageBegin{
			ItemID:    id,
			Hash:      item.Hash[:],
			Width:     uint32(item.Width),
			Height:    uint32(item.Height),
			TotalSize: uint64(len(item.PNG)),
			Chunks:    uint32(len(chunks)),
			CreatedAt: now,
			SourceApp: clip.CleanSource(item.SourceApp),
		}); err != nil {
			return err
		}
		for i, part := range chunks {
			if err := s.write(wire.TypeImageChunk, wire.ImageChunk{
				ItemID: id,
				Index:  uint32(i),
				Data:   part,
			}); err != nil {
				return err
			}
		}
		return s.write(wire.TypeImageEnd, wire.ImageEnd{ItemID: id})
	default:
		return nil
	}
}

func (s *Session) write(typ byte, v any) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return io.ErrClosedPipe
	}
	// Do not hold s.mu across the write. Close must be able to close the
	// connection while a write is blocked, or the two goroutines deadlock.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return wire.WriteMsg(s.conn, typ, v)
}

type assembler struct {
	mu       sync.Mutex
	begin    wire.ImageBegin
	data     []byte
	chunks   uint32
	deadline time.Time
	timer    *time.Timer
	failed   bool
	onExpire func()
}

func (a *assembler) arm(onExpire func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onExpire = onExpire
	a.deadline = time.Now().Add(imageAssembleWait)
	a.timer = time.AfterFunc(imageAssembleWait, a.expire)
}

func (a *assembler) expire() {
	a.mu.Lock()
	if a.timer == nil {
		a.mu.Unlock()
		return
	}
	if time.Now().Before(a.deadline) {
		wait := time.Until(a.deadline)
		a.timer = time.AfterFunc(wait, a.expire)
		a.mu.Unlock()
		return
	}
	a.data = nil
	a.failed = true
	fn := a.onExpire
	a.onExpire = nil
	a.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (a *assembler) stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	timer := a.timer
	a.timer = nil
	a.onExpire = nil
	a.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

func (a *assembler) failedNow() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.failed
}

func (a *assembler) buffered() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.data)
}

func newAssembler(begin wire.ImageBegin) (*assembler, error) {
	if begin.TotalSize == 0 || begin.TotalSize > clip.MaxImageBytes ||
		begin.Chunks != uint32((begin.TotalSize+wire.ChunkSize-1)/wire.ChunkSize) || len(begin.Hash) != 32 {
		return nil, errors.New("无效的图片大小、分片数量或哈希")
	}
	return &assembler{begin: begin, deadline: time.Now().Add(imageAssembleWait)}, nil
}

func (a *assembler) add(chunk wire.ImageChunk) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed {
		return errors.New("图片接收超时")
	}
	if chunk.ItemID != a.begin.ItemID || chunk.Index != a.chunks || a.chunks >= a.begin.Chunks {
		return errors.New("图片分片顺序错误")
	}
	if len(chunk.Data) == 0 || len(chunk.Data) > wire.ChunkSize || uint64(len(a.data)+len(chunk.Data)) > a.begin.TotalSize {
		return errors.New("图片分片大小超出限制")
	}
	if a.data == nil {
		// A header alone must not reserve a full-size image buffer.
		a.data = make([]byte, 0, int(a.begin.TotalSize))
	}
	a.data = append(a.data, chunk.Data...)
	a.chunks++
	a.deadline = time.Now().Add(imageAssembleWait)
	return nil
}

func (a *assembler) finish() (clip.Item, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed {
		return clip.Item{}, errors.New("图片接收超时")
	}
	if a.chunks != a.begin.Chunks {
		return clip.Item{}, fmt.Errorf("分片数量 %d，期望 %d", a.chunks, a.begin.Chunks)
	}
	if uint64(len(a.data)) != a.begin.TotalSize {
		return clip.Item{}, fmt.Errorf("图片大小 %d，期望 %d", len(a.data), a.begin.TotalSize)
	}
	item, err := clip.FromPNG(a.data)
	if err != nil {
		return clip.Item{}, err
	}
	if !bytes.Equal(item.Hash[:], a.begin.Hash) {
		return clip.Item{}, errors.New("图片哈希不一致")
	}
	item.SourceApp = clip.CleanSource(a.begin.SourceApp)
	return item, nil
}

func textFromWire(payload []byte) (clip.Item, error) {
	var msg wire.Text
	if err := wire.Unmarshal(payload, &msg); err != nil {
		return clip.Item{}, err
	}
	item, err := clip.FromText(msg.Text)
	if err != nil {
		return clip.Item{}, err
	}
	// Protocol v1 hashes the transmitted bytes. Verify before using the
	// normalized hash so older peers can still send CRLF text.
	wireHash := blake3.Sum256([]byte(msg.Text))
	if len(msg.Hash) != 32 || !bytes.Equal(wireHash[:], msg.Hash) {
		return clip.Item{}, errors.New("文本哈希不一致")
	}
	item.SourceApp = clip.CleanSource(msg.SourceApp)
	return item, nil
}

func itemIDOf(payload []byte) string {
	var probe struct {
		ItemID string `msgpack:"item_id"`
	}
	_ = wire.Unmarshal(payload, &probe)
	return probe.ItemID
}

func split(b []byte, n int) [][]byte {
	if n <= 0 {
		n = wire.ChunkSize
	}
	if len(b) == 0 {
		return [][]byte{{}}
	}
	var out [][]byte
	for len(b) > 0 {
		end := n
		if end > len(b) {
			end = len(b)
		}
		out = append(out, b[:end])
		b = b[end:]
	}
	return out
}

func peerCert(conn *tls.Conn) (*identityResult, error) {
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, errors.New("对端没有证书")
	}
	leaf := state.PeerCertificates[0]
	id, err := identity.DeviceID(leaf)
	if err != nil {
		return nil, err
	}
	return &identityResult{id: id, fp: identity.Fingerprint(leaf)}, nil
}

type identityResult struct {
	id string
	fp string
}
