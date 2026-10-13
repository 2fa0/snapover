// Package wire is the length-prefixed MessagePack framing used between peers.
// One frame is at most a little over 1 MiB. Images are split into chunks.
package wire

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/vmihailenco/msgpack/v5"
)

const (
	// MaxFrame is the type byte plus payload. Text is capped at 1 MiB;
	// the extra kibibyte is the MessagePack envelope.
	MaxFrame = 1<<20 + 1024
	// ChunkSize is the PNG slice carried by one ImageChunk.
	ChunkSize = 256 << 10

	Proto uint16 = 1

	TypeHello      byte = 1
	TypeText       byte = 2
	TypeImageBegin byte = 3
	TypeImageChunk byte = 4
	TypeImageEnd   byte = 5
	TypeAck        byte = 6
	TypePing       byte = 7
	TypePong       byte = 8
	TypeError      byte = 9
	TypeFileBegin  byte = 10
	TypeFileChunk  byte = 11
	TypeFileEnd    byte = 12
	TypeFileResult byte = 13
	TypeFileCancel byte = 14
	TypePairState  byte = 15
	TypePairChoice byte = 16
	TypePairReady  byte = 17
)

type Hello struct {
	DeviceID     string   `msgpack:"device_id"`
	Name         string   `msgpack:"name"`
	Platform     string   `msgpack:"platform"`
	Proto        uint16   `msgpack:"proto"`
	Fingerprint  string   `msgpack:"fingerprint"`
	ListenPort   string   `msgpack:"listen_port,omitempty"`
	Capabilities []string `msgpack:"capabilities,omitempty"`
}

const FileCapability = "file-transfer-v1"

const PairCapability = "pair-confirm-v1"

type PairState struct {
	Trusted bool `msgpack:"trusted"`
}

type PairChoice struct {
	Accept bool `msgpack:"accept"`
}

type FileBegin struct {
	ItemID    string `msgpack:"item_id"`
	Name      string `msgpack:"name"`
	TotalSize int64  `msgpack:"total_size"`
}

type FileChunk struct {
	ItemID string `msgpack:"item_id"`
	Offset int64  `msgpack:"offset"`
	Data   []byte `msgpack:"data"`
}

type FileEnd struct {
	ItemID string `msgpack:"item_id"`
	Hash   []byte `msgpack:"hash"`
}

type FileResult struct {
	ItemID  string `msgpack:"item_id"`
	State   string `msgpack:"state"` // ready, complete, error
	Message string `msgpack:"message,omitempty"`
}

type Text struct {
	ItemID    string `msgpack:"item_id"`
	Hash      []byte `msgpack:"hash"`
	Text      string `msgpack:"text"`
	CreatedAt int64  `msgpack:"created_at"`
	SourceApp string `msgpack:"source_app,omitempty"`
}

type ImageBegin struct {
	ItemID    string `msgpack:"item_id"`
	Hash      []byte `msgpack:"hash"`
	Width     uint32 `msgpack:"width"`
	Height    uint32 `msgpack:"height"`
	TotalSize uint64 `msgpack:"total_size"`
	Chunks    uint32 `msgpack:"chunks"`
	CreatedAt int64  `msgpack:"created_at"`
	SourceApp string `msgpack:"source_app,omitempty"`
}

type ImageChunk struct {
	ItemID string `msgpack:"item_id"`
	Index  uint32 `msgpack:"index"`
	Data   []byte `msgpack:"data"`
}

type ImageEnd struct {
	ItemID string `msgpack:"item_id"`
}

type Ack struct {
	ItemID string `msgpack:"item_id"`
}

type Error struct {
	Code uint16 `msgpack:"code"`
	Msg  string `msgpack:"msg"`
}

func WriteMsg(w io.Writer, typ byte, v any) error {
	var payload []byte
	if v != nil {
		var err error
		payload, err = msgpack.Marshal(v)
		if err != nil {
			return err
		}
	}
	if len(payload)+1 > MaxFrame {
		return fmt.Errorf("帧长度 %d 超过 %d", len(payload)+1, MaxFrame)
	}
	frame := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)+1))
	frame[4] = typ
	copy(frame[5:], payload)
	_, err := w.Write(frame)
	return err
}

func ReadFrame(r io.Reader) (byte, []byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || int(n) > MaxFrame {
		return 0, nil, fmt.Errorf("非法帧长度 %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

func Unmarshal(payload []byte, v any) error {
	return msgpack.Unmarshal(payload, v)
}
