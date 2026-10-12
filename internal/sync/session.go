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

	"github.com/2fa0/clipbridge/internal/clip"
	"github.com/2fa0/clipbridge/internal/identity"
	"github.com/2fa0/clipbridge/internal/wire"
	"github.com/oklog/ulid/v2"
)

const (
	pingEvery = 15 * time.Second
	readLimit = 45 * time.Second
	queueSize = 8
)

// Session is one live peer connection after Hello has been accepted.
type Session struct {
	conn     *tls.Conn
	peerID   string
	weDialed bool
	log      *slog.Logger
	onItem   func(clip.Item)
	out      chan clip.Item

	mu       sync.Mutex
	writeMu  sync.Mutex
	closed   bool
	lastRecv time.Time
	done     chan struct{}
	once     sync.Once
}

func newSession(conn *tls.Conn, peerID string, weDialed bool, log *slog.Logger, onItem func(clip.Item)) *Session {
	return &Session{
		conn:     conn,
		peerID:   peerID,
		weDialed: weDialed,
		log:      log,
		onItem:   onItem,
		out:      make(chan clip.Item, queueSize),
		lastRecv: time.Now(),
		done:     make(chan struct{}),
	}
}

func (s *Session) enqueue(item clip.Item) bool {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return false
	}
	select {
	case s.out <- item:
		return true
	default:
		return false
	}
}

func (s *Session) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
		_ = s.conn.Close()
	})
}

func (s *Session) run() {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.readLoop()
	}()
	go func() {
		defer wg.Done()
		s.writeLoop()
	}()
	wg.Wait()
}

func (s *Session) readLoop() {
	defer s.Close()
	var asm *assembler
	for {
		typ, payload, err := wire.ReadFrame(s.conn)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
				s.log.Debug("读连接结束", "peer", s.peerID, "err", err)
			}
			return
		}
		s.mu.Lock()
		s.lastRecv = time.Now()
		s.mu.Unlock()
		switch typ {
		case wire.TypePing:
			_ = s.write(wire.TypePong, nil)
		case wire.TypePong, wire.TypeAck:
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
			s.onItem(item)
			_ = s.write(wire.TypeAck, wire.Ack{ItemID: itemIDOf(payload)})
		case wire.TypeImageBegin:
			var begin wire.ImageBegin
			if err := wire.Unmarshal(payload, &begin); err != nil {
				s.log.Warn("图片头无效", "err", err)
				continue
			}
			asm = &assembler{begin: begin}
			if begin.Chunks == 0 || begin.TotalSize == 0 || begin.TotalSize > clip.MaxImageBytes {
				s.log.Warn("图片头超出限制", "size", begin.TotalSize, "chunks", begin.Chunks)
				asm = nil
			}
		case wire.TypeImageChunk:
			if asm == nil {
				continue
			}
			var chunk wire.ImageChunk
			if err := wire.Unmarshal(payload, &chunk); err != nil {
				asm = nil
				continue
			}
			if chunk.ItemID != asm.begin.ItemID || chunk.Index != uint32(len(asm.parts)) {
				s.log.Warn("图片分片顺序错误", "want", len(asm.parts), "got", chunk.Index)
				asm = nil
				continue
			}
			asm.parts = append(asm.parts, chunk.Data)
			asm.size += len(chunk.Data)
			if asm.size > clip.MaxImageBytes {
				s.log.Warn("图片分片总量超限")
				asm = nil
			}
		case wire.TypeImageEnd:
			if asm == nil {
				continue
			}
			var end wire.ImageEnd
			if err := wire.Unmarshal(payload, &end); err != nil || end.ItemID != asm.begin.ItemID {
				asm = nil
				continue
			}
			item, err := asm.finish()
			asm = nil
			if err != nil {
				s.log.Warn("丢弃图片", "err", err)
				continue
			}
			s.onItem(item)
			_ = s.write(wire.TypeAck, wire.Ack{ItemID: itemIDOf(payload)})
		default:
			s.log.Debug("忽略未知帧", "type", typ)
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
		case item := <-s.out:
			if err := s.sendItem(item); err != nil {
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

func (s *Session) sendItem(item clip.Item) error {
	now := time.Now().UnixMilli()
	id := ulid.Make().String()
	switch item.Kind {
	case clip.KindText:
		return s.write(wire.TypeText, wire.Text{
			ItemID:    id,
			Hash:      item.Hash[:],
			Text:      item.Text,
			CreatedAt: now,
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
	return wire.WriteMsg(s.conn, typ, v)
}

type assembler struct {
	begin wire.ImageBegin
	parts [][]byte
	size  int
}

func (a *assembler) finish() (clip.Item, error) {
	if uint32(len(a.parts)) != a.begin.Chunks {
		return clip.Item{}, fmt.Errorf("分片数量 %d，期望 %d", len(a.parts), a.begin.Chunks)
	}
	if uint64(a.size) != a.begin.TotalSize {
		return clip.Item{}, fmt.Errorf("图片大小 %d，期望 %d", a.size, a.begin.TotalSize)
	}
	png := make([]byte, 0, a.size)
	for _, part := range a.parts {
		png = append(png, part...)
	}
	item, err := clip.FromPNG(png)
	if err != nil {
		return clip.Item{}, err
	}
	if len(a.begin.Hash) != 32 || !bytes.Equal(item.Hash[:], a.begin.Hash) {
		return clip.Item{}, errors.New("图片哈希不一致")
	}
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
	if len(msg.Hash) != 32 || !bytes.Equal(item.Hash[:], msg.Hash) {
		return clip.Item{}, errors.New("文本哈希不一致")
	}
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
