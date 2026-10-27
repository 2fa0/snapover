// Package wire is the length-prefixed MessagePack framing used between peers.
// One frame is at most a little over 20 MiB. Images are split into chunks.
package wire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

const (
	// MaxFrame is the type byte plus payload. Text is capped at 20 MiB;
	// the extra kibibyte is the MessagePack envelope.
	MaxFrame = 20<<20 + 1024
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

const FileClipboardCapability = "file-clipboard-v1"

const PairCapability = "pair-confirm-v1"

const ClipboardResultCapability = "clipboard-result-v1"

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
	Clipboard bool   `msgpack:"clipboard,omitempty"`
	SourceApp string `msgpack:"source_app,omitempty"`
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
	ItemID  string `msgpack:"item_id"`
	State   string `msgpack:"state,omitempty"` // complete, history, error; absent on older peers
	Message string `msgpack:"message,omitempty"`
}

type Error struct {
	Code uint16 `msgpack:"code"`
	Msg  string `msgpack:"msg"`
}

func WriteMsg(w io.Writer, typ byte, v any) error {
	// Reserve the header in the encoded buffer so large text is not copied
	// into a second, equally large frame before every socket write.
	frame := bytes.NewBuffer(make([]byte, 5, 256))
	if v != nil {
		enc := msgpack.GetEncoder()
		enc.Reset(frame)
		err := enc.Encode(v)
		msgpack.PutEncoder(enc)
		if err != nil {
			return err
		}
	}
	if frame.Len()-4 > MaxFrame {
		return fmt.Errorf("帧长度 %d 超过 %d", frame.Len()-4, MaxFrame)
	}
	header := frame.Bytes()[:5]
	binary.BigEndian.PutUint32(header[:4], uint32(frame.Len()-4))
	header[4] = typ
	_, err := frame.WriteTo(w)
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
	if len(payload) > MaxFrame-1 {
		return fmt.Errorf("消息超过帧长度限制")
	}
	r := bytes.NewReader(payload)
	if err := checkValue(msgpack.NewDecoder(r), r, 0); err != nil {
		return err
	}
	if r.Len() != 0 {
		return fmt.Errorf("消息包含多余内容")
	}
	return msgpack.Unmarshal(payload, v)
}

// Validate before decoding: unknown fields otherwise recurse without a bound,
// and binary fields allocate their declared length before reading their bytes.
// Keep skipping unknown fields for protocol compatibility.
func checkValue(d *msgpack.Decoder, r *bytes.Reader, depth int) error {
	if depth > 32 {
		return fmt.Errorf("消息嵌套过深")
	}
	c, err := d.PeekCode()
	if err != nil {
		return err
	}
	var n int
	switch {
	case msgpcode.IsFixedArray(c) || c == msgpcode.Array16 || c == msgpcode.Array32:
		n, err = d.DecodeArrayLen()
	case msgpcode.IsFixedMap(c) || c == msgpcode.Map16 || c == msgpcode.Map32:
		n, err = d.DecodeMapLen()
		if n > r.Len()/2 {
			return io.ErrUnexpectedEOF
		}
		n *= 2
	case msgpcode.IsString(c) || msgpcode.IsBin(c) || msgpcode.IsExt(c):
		if msgpcode.IsExt(c) {
			_, n, err = d.DecodeExtHeader()
		} else {
			n, err = d.DecodeBytesLen()
		}
		if err != nil {
			return err
		}
		if n < 0 || n > r.Len() {
			return io.ErrUnexpectedEOF
		}
		_, err = r.Seek(int64(n), io.SeekCurrent)
		return err
	default:
		return d.Skip()
	}
	if err != nil {
		return err
	}
	if n < 0 || n > r.Len() {
		return io.ErrUnexpectedEOF
	}
	for range n {
		if err := checkValue(d, r, depth+1); err != nil {
			return err
		}
	}
	return nil
}
