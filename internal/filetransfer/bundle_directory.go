package filetransfer

import (
	"encoding/binary"
	"errors"
	"io"
)

// Validate the central directory before archive/zip allocates File values.
// Checking len(Reader.File) afterwards would allow millions of tiny entries
// to exhaust memory first. Our bundles have no archive comment or trailing data.
func checkBundleDirectory(r io.ReaderAt, size int64) error {
	bad := errors.New("文件归档目录无效或超过限制")
	if size < 22 {
		return bad
	}
	var end [22]byte
	if _, err := r.ReadAt(end[:], size-22); err != nil {
		return err
	}
	u16 := binary.LittleEndian.Uint16
	u32 := binary.LittleEndian.Uint32
	u64 := binary.LittleEndian.Uint64
	if u32(end[:4]) != 0x06054b50 || u16(end[4:6]) != 0 || u16(end[6:8]) != 0 || u16(end[20:22]) != 0 {
		return bad
	}
	count := uint64(u16(end[10:12]))
	length := uint64(u32(end[12:16]))
	offset := uint64(u32(end[16:20]))
	boundary := uint64(size - 22)
	if count == 0xffff || length == 0xffffffff || offset == 0xffffffff {
		if size < 98 {
			return bad
		}
		var locator [20]byte
		if _, err := r.ReadAt(locator[:], size-42); err != nil {
			return err
		}
		if u32(locator[:4]) != 0x07064b50 || u32(locator[4:8]) != 0 || u32(locator[16:20]) != 1 {
			return bad
		}
		zip64Offset := u64(locator[8:16])
		if zip64Offset != uint64(size-98) {
			return bad
		}
		var large [56]byte
		if _, err := r.ReadAt(large[:], int64(zip64Offset)); err != nil {
			return err
		}
		if u32(large[:4]) != 0x06064b50 || u64(large[4:12]) != 44 || u32(large[16:20]) != 0 || u32(large[20:24]) != 0 || u64(large[24:32]) != u64(large[32:40]) {
			return bad
		}
		count, length, offset = u64(large[32:40]), u64(large[40:48]), u64(large[48:56])
		boundary = zip64Offset
	} else if u16(end[8:10]) != u16(end[10:12]) {
		return bad
	}
	if count == 0 || count > MaxBundleEntries || length > uint64(bundleOverhead) || offset > boundary || length != boundary-offset {
		return bad
	}
	var header [46]byte
	var used uint64
	for i := uint64(0); i < count; i++ {
		if length-used < 46 {
			return bad
		}
		if _, err := r.ReadAt(header[:], int64(offset+used)); err != nil {
			return err
		}
		if u32(header[:4]) != 0x02014b50 || u16(header[34:36]) != 0 {
			return bad
		}
		name := uint64(u16(header[28:30]))
		extra := uint64(u16(header[30:32]))
		comment := uint64(u16(header[32:34]))
		entry := 46 + name + extra + comment
		if name == 0 || name > MaxBundlePath+1 || entry > length-used {
			return bad
		}
		used += entry
	}
	if used != length {
		return bad
	}
	return nil
}
