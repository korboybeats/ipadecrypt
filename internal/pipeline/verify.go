package pipeline

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	mhMagic    = 0xfeedface
	mhMagic64  = 0xfeedfacf
	mhCigam    = 0xcefaedfe
	mhCigam64  = 0xcffaedfe
	fatMagic   = 0xcafebabe
	fatCigam   = 0xbebafeca
	fatMagic64 = 0xcafebabf
	fatCigam64 = 0xbfbafeca

	lcEncryptionInfo   = 0x21
	lcEncryptionInfo64 = 0x2c
)

type VerifyResult struct {
	Scanned   int
	Encrypted []string
	Skipped   []string
}

func VerifyCryptid(ipaPath string) (VerifyResult, error) {
	var res VerifyResult

	r, err := zip.OpenReader(ipaPath)
	if err != nil {
		return res, fmt.Errorf("open %s: %w", ipaPath, err)
	}
	defer r.Close()

	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, "Payload/") {
			continue
		}

		if f.FileInfo().IsDir() {
			continue
		}
		// Cheap filter: skip non-Mach-O files (plists, images, etc.) with one short read.
		rc, err := f.Open()
		if err != nil {
			return res, fmt.Errorf("open %s: %w", f.Name, err)
		}

		head := make([]byte, 4)
		if n, _ := io.ReadFull(rc, head); n < 4 || !isMachOMagic(head) {
			rc.Close()
			continue
		}

		rest, err := io.ReadAll(rc)
		rc.Close()

		if err != nil {
			return res, fmt.Errorf("read %s: %w", f.Name, err)
		}

		data := append(head, rest...)

		encrypted, err := sliceHasCryptid(data)
		if err != nil {
			res.Skipped = append(res.Skipped, f.Name)
			continue
		}

		res.Scanned++
		if encrypted {
			res.Encrypted = append(res.Encrypted, f.Name)
		}
	}

	return res, nil
}

func isMachOMagic(b []byte) bool {
	if len(b) < 4 {
		return false
	}

	m := binary.LittleEndian.Uint32(b)

	switch m {
	case mhMagic, mhMagic64, mhCigam, mhCigam64, fatMagic, fatCigam, fatMagic64, fatCigam64:
		return true
	}

	return false
}

func sliceHasCryptid(data []byte) (bool, error) {
	if len(data) < 4 {
		return false, errors.New("too short")
	}

	magic := binary.LittleEndian.Uint32(data[:4])
	switch magic {
	case fatMagic, fatCigam:
		return checkFat(data, false)
	case fatMagic64, fatCigam64:
		return checkFat(data, true)
	case mhMagic, mhMagic64:
		return checkThin(data, false)
	case mhCigam, mhCigam64:
		return checkThin(data, true)
	}

	return false, errors.New("not mach-o")
}

func checkFat(data []byte, is64 bool) (bool, error) {
	bo := binary.BigEndian // fat headers are always big-endian on disk regardless of arch

	if len(data) < 8 {
		return false, errors.New("fat header truncated")
	}

	nfat := bo.Uint32(data[4:8])
	if nfat == 0 || nfat > 32 {
		return false, fmt.Errorf("implausible nfat_arch=%d", nfat)
	}

	archSize := 20
	if is64 {
		archSize = 32
	}

	off := 8
	for range nfat {
		if off+archSize > len(data) {
			return false, errors.New("fat_arch truncated")
		}

		var sliceOff, sliceSize uint64
		if is64 {
			sliceOff = bo.Uint64(data[off+8 : off+16])
			sliceSize = bo.Uint64(data[off+16 : off+24])
		} else {
			sliceOff = uint64(bo.Uint32(data[off+8 : off+12]))
			sliceSize = uint64(bo.Uint32(data[off+12 : off+16]))
		}

		off += archSize

		if sliceOff+sliceSize > uint64(len(data)) {
			return false, errors.New("fat slice out of range")
		}

		if sliceSize < 4 {
			continue
		}

		slice := data[sliceOff : sliceOff+sliceSize]
		m := binary.LittleEndian.Uint32(slice[:4])

		enc, err := checkThin(slice, m == mhCigam || m == mhCigam64)
		if err != nil {
			return false, err
		}

		if enc {
			return true, nil
		}
	}

	return false, nil
}

func checkThin(data []byte, swap bool) (bool, error) {
	var bo binary.ByteOrder = binary.LittleEndian
	if swap {
		bo = binary.BigEndian
	}

	if len(data) < 28 {
		return false, errors.New("mach_header truncated")
	}

	magic := bo.Uint32(data[0:4])
	is64 := magic == mhMagic64 || magic == mhCigam64
	ncmds := bo.Uint32(data[16:20])
	sizeofcmds := bo.Uint32(data[20:24])

	headerSize := 28
	if is64 {
		headerSize = 32
	}

	if uint64(headerSize)+uint64(sizeofcmds) > uint64(len(data)) {
		return false, errors.New("load commands truncated")
	}

	if ncmds > 1<<16 {
		return false, fmt.Errorf("implausible ncmds=%d", ncmds)
	}

	p := headerSize
	for i := uint32(0); i < ncmds; i++ {
		if p+8 > len(data) {
			return false, errors.New("load cmd truncated")
		}

		cmd := bo.Uint32(data[p : p+4])

		cmdSize := bo.Uint32(data[p+4 : p+8])
		if cmdSize < 8 || int(cmdSize) > len(data)-p {
			return false, fmt.Errorf("bad cmdsize=%d", cmdSize)
		}

		if (is64 && cmd == lcEncryptionInfo64) || (!is64 && cmd == lcEncryptionInfo) {
			if int(cmdSize) < 20 {
				return false, errors.New("LC_ENCRYPTION_INFO truncated")
			}

			cryptid := bo.Uint32(data[p+16 : p+20])
			if cryptid != 0 {
				return true, nil
			}
		}

		p += int(cmdSize)
	}

	return false, nil
}
