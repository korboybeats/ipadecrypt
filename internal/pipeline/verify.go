package pipeline

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/londek/ipadecrypt/internal/macho"
)

type VerifyMismatch struct {
	Name   string
	Reason string
}

type VerifyResult struct {
	Scanned        int              // Mach-Os parsed in output IPA
	Compared       int              // additionally byte-checked against source
	StillEncrypted []string         // cryptid != 0 (decrypt didn't write through)
	AllZeroCrypt   []string         // cryptid == 0 but crypt region all zeros (decrypt-on-fault never fired)
	Mismatches     []VerifyMismatch // bytes outside crypt region differ from source
	Missing        []string         // output entry has no source counterpart (only when source given)
	Skipped        []string         // output Mach-O failed to parse
}

func (r VerifyResult) OK() bool {
	return len(r.StillEncrypted) == 0 &&
		len(r.AllZeroCrypt) == 0 &&
		len(r.Mismatches) == 0
}

// Verify scans every Mach-O in the decrypted output IPA, asserting
// cryptid==0 and that the FairPlay crypt region isn't a zero-fill (a
// signal that helper decrypt-on-fault never actually fired). When
// sourceIPA is non-empty, each output is also byte-compared against
// its source slice outside the cryptid byte and the encrypted region,
// catching cryptid-zeroed-without-decrypting and any out-of-band byte
// corruption. When skipAppex is true, entries under Payload/<App>.app
// /PlugIns/ are ignored - the helper left them encrypted on purpose.
//
// Output is THIN - helper drops fat siblings - so each output entry
// maps to one specific slice in a (possibly fat) source.
func Verify(outputIPA, sourceIPA string, skipAppex bool) (VerifyResult, error) {
	var res VerifyResult

	out, err := zip.OpenReader(outputIPA)
	if err != nil {
		return res, fmt.Errorf("open output %s: %w", outputIPA, err)
	}
	defer out.Close()

	var srcByName map[string]*zip.File

	if sourceIPA != "" {
		src, err := zip.OpenReader(sourceIPA)
		if err != nil {
			return res, fmt.Errorf("open source %s: %w", sourceIPA, err)
		}
		defer src.Close()

		srcByName = make(map[string]*zip.File, len(src.File))
		for _, f := range src.File {
			srcByName[f.Name] = f
		}
	}

	for _, of := range out.File {
		if !strings.HasPrefix(of.Name, "Payload/") || of.FileInfo().IsDir() {
			continue
		}

		if skipAppex && isAppExtPath(of.Name) {
			continue
		}

		outData, isMacho, err := readMachO(of)
		if err != nil {
			return res, fmt.Errorf("read %s: %w", of.Name, err)
		}

		if !isMacho {
			continue
		}

		res.Scanned++

		encrypted, err := macho.SliceHasCryptid(outData)
		if err != nil {
			res.Skipped = append(res.Skipped, of.Name)
			continue
		}

		if encrypted {
			res.StillEncrypted = append(res.StillEncrypted, of.Name)
			continue
		}

		// Source-bearing path: precise compare (handles its own all-zero
		// check, gated on srcCrypt.Cryptid to avoid false positives on
		// legitimately-plaintext passthrough slices).
		if sf := srcByName[of.Name]; sf != nil {
			srcData, srcIsMacho, err := readMachO(sf)
			if err != nil {
				return res, fmt.Errorf("read source %s: %w", of.Name, err)
			}

			// Source isn't Mach-O (TBD stubs, .a archives, resolved
			// symlinks) - nothing FairPlay touches, skip.
			if !srcIsMacho {
				continue
			}

			if reason := compareMachOSlice(outData, srcData); reason != "" {
				res.Mismatches = append(res.Mismatches, VerifyMismatch{Name: of.Name, Reason: reason})
				continue
			}

			res.Compared++

			continue
		}

		if srcByName != nil {
			res.Missing = append(res.Missing, of.Name)
		}

		// Source-free fallback: flag thin outputs whose entire crypt
		// region is zeros. False positives are possible if the source
		// shipped LC_ENCRYPTION_INFO with cryptid==0 over a genuinely
		// zero region - rare, and source-aware verify catches it.
		if cryptZeroed(outData) {
			res.AllZeroCrypt = append(res.AllZeroCrypt, of.Name)
		}
	}

	return res, nil
}

func cryptZeroed(data []byte) bool {
	if len(data) < 4 {
		return false
	}

	m := binary.LittleEndian.Uint32(data[:4])
	if m != macho.MagicLE && m != macho.Magic64LE {
		return false
	}

	info, err := macho.ReadEncryptionInfo(data)
	if err != nil || info.CryptSize == 0 {
		return false
	}

	return isAllZero(data[info.CryptOff : info.CryptOff+info.CryptSize])
}

// readMachO returns the file's bytes if it begins with a Mach-O / fat magic.
// (nil, false, nil) signals "not a Mach-O, skip".
func readMachO(f *zip.File) ([]byte, bool, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, false, err
	}
	defer rc.Close()

	head := make([]byte, 4)
	if n, _ := io.ReadFull(rc, head); n < 4 || !macho.IsMagic(head) {
		return nil, false, nil
	}

	rest, err := io.ReadAll(rc)
	if err != nil {
		return nil, false, err
	}

	return append(head, rest...), true, nil
}

// isAppExtPath matches Payload/<App>.app/PlugIns/<*>.appex/...
func isAppExtPath(name string) bool {
	parts := strings.Split(name, "/")

	return len(parts) >= 4 &&
		parts[0] == "Payload" &&
		strings.HasSuffix(parts[1], ".app") &&
		parts[2] == "PlugIns"
}

// compareMachOSlice returns "" when the output is consistent with the
// source. Two cases:
//   - Output fat → helper didn't touch (no FairPlay slice); must match
//     source verbatim.
//   - Output thin → helper decrypted one slice and dropped fat siblings;
//     pick the matching cputype slice in source and byte-compare outside
//     the encrypted region + cryptid byte.
func compareMachOSlice(outData, srcData []byte) string {
	if len(outData) < 4 {
		return "output too short"
	}

	outMagic := binary.LittleEndian.Uint32(outData[:4])
	outIsFat := outMagic == macho.FatMagic || outMagic == macho.FatCigam ||
		outMagic == macho.FatMagic64 || outMagic == macho.FatCigam64

	if outIsFat {
		if !bytes.Equal(outData, srcData) {
			return "fat passthrough differs from source"
		}

		return ""
	}

	outCpu, outSub, err := macho.ReadThinCpu(outData)
	if err != nil {
		return "parse output cpu: " + err.Error()
	}

	srcSlice, err := macho.PickSlice(srcData, outCpu, outSub)
	if err != nil {
		return "match source slice: " + err.Error()
	}

	outCrypt, err := macho.ReadEncryptionInfo(outData)
	if err != nil {
		// No LC_ENCRYPTION_INFO in output - slice was never encrypted.
		// Direct compare without the cryptid/cryptoff skip.
		if bytes.Equal(outData, srcSlice) {
			return ""
		}

		return "thin passthrough differs from source slice"
	}

	srcCrypt, err := macho.ReadEncryptionInfo(srcSlice)
	if err != nil {
		return "parse source LC_ENCRYPTION_INFO: " + err.Error()
	}

	// Helper only patches cryptid and decrypts bytes - never moves
	// cryptoff or cryptsize.
	if outCrypt.CryptOff != srcCrypt.CryptOff ||
		outCrypt.CryptSize != srcCrypt.CryptSize {
		return fmt.Sprintf(
			"cryptoff/cryptsize moved: output=(%d,%d) source=(%d,%d)",
			outCrypt.CryptOff, outCrypt.CryptSize,
			srcCrypt.CryptOff, srcCrypt.CryptSize,
		)
	}

	if len(outData) != len(srcSlice) {
		return fmt.Sprintf("size differs: output=%d source=%d", len(outData), len(srcSlice))
	}

	cryptidEnd := outCrypt.CryptidOffset + 4
	cryptEnd := outCrypt.CryptOff + outCrypt.CryptSize

	if !bytes.Equal(outData[:outCrypt.CryptidOffset], srcSlice[:outCrypt.CryptidOffset]) {
		return "header diff before cryptid"
	}

	if !bytes.Equal(outData[cryptidEnd:outCrypt.CryptOff], srcSlice[cryptidEnd:outCrypt.CryptOff]) {
		return "header/load-commands diff between cryptid and cryptoff"
	}

	if cryptEnd < uint64(len(outData)) &&
		!bytes.Equal(outData[cryptEnd:], srcSlice[cryptEnd:]) {
		return "diff after encrypted region (LINKEDIT/etc)"
	}

	outCryptBytes := outData[outCrypt.CryptOff:cryptEnd]
	srcCryptBytes := srcSlice[srcCrypt.CryptOff : srcCrypt.CryptOff+srcCrypt.CryptSize]

	// Source already plaintext: bytes must match verbatim, and the
	// "decrypt bailed" heuristics below would false-flag.
	if srcCrypt.Cryptid == 0 {
		return ""
	}

	if isAllZero(outCryptBytes) {
		return "crypt region is all zeros (FairPlay decrypt-on-fault never fired in target)"
	}

	if bytes.Equal(outCryptBytes, srcCryptBytes) {
		return "crypt region byte-equal to source ciphertext (cryptid zeroed without decrypting)"
	}

	return ""
}

func isAllZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}

	return true
}
