//go:build spike && windows

package policyspike

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

// Registry.pol types (MS-GPREG).
const (
	regSZ     uint32 = 1
	regExpand uint32 = 2
	regBin    uint32 = 3
	regDWORD  uint32 = 4
	regMulti  uint32 = 7
)

var polSignature = []byte{'P', 'R', 'e', 'g'}

// PolEntry is one Registry.pol instruction.
type PolEntry struct {
	Key   string
	Value string
	Type  uint32
	Data  []byte
}

// EncodePol builds a Registry.pol body (MS-GPREG).
// Key/Value/brackets/semicolons are UTF-16LE; Type/Size are uint32 LE; Data is raw.
func EncodePol(entries []PolEntry) []byte {
	var b bytes.Buffer
	b.Write(polSignature)
	_ = binary.Write(&b, binary.LittleEndian, uint32(1))
	for _, e := range entries {
		writeUTF16Z(&b, "[")
		writeUTF16Z(&b, e.Key)
		writeUTF16Z(&b, ";")
		writeUTF16Z(&b, e.Value)
		writeUTF16Z(&b, ";")
		_ = binary.Write(&b, binary.LittleEndian, e.Type)
		writeUTF16Z(&b, ";")
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(e.Data)))
		writeUTF16Z(&b, ";")
		b.Write(e.Data)
		writeUTF16Z(&b, "]")
	}
	return b.Bytes()
}

func writeUTF16Z(b *bytes.Buffer, s string) {
	b.Write(encodeUTF16Z(s))
}

func encodeUTF16Z(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, (len(u)+1)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(out[i*2:], v)
	}
	return out
}

func dwordData(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func szData(s string) []byte {
	return encodeUTF16Z(s)
}

// ParsePol reads Registry.pol entries (best-effort for spike assertions).
func ParsePol(data []byte) ([]PolEntry, error) {
	if len(data) < 8 || !bytes.Equal(data[:4], polSignature) {
		return nil, fmt.Errorf("bad Registry.pol signature")
	}
	ver := binary.LittleEndian.Uint32(data[4:8])
	if ver != 1 {
		return nil, fmt.Errorf("unsupported Registry.pol version %d", ver)
	}
	i := 8
	var out []PolEntry
	for i < len(data) {
		if _, n, err := readUTF16Z(data[i:]); err != nil || n == 0 {
			break
		} else {
			// expect "["
			s, n, err := readUTF16Z(data[i:])
			if err != nil {
				return out, err
			}
			if s != "[" {
				return out, fmt.Errorf("expected '[' at %d, got %q", i, s)
			}
			i += n
		}
		key, n, err := readUTF16Z(data[i:])
		if err != nil {
			return out, err
		}
		i += n
		semi, n, err := readUTF16Z(data[i:])
		if err != nil || semi != ";" {
			return out, fmt.Errorf("expected ';' after key")
		}
		i += n
		val, n, err := readUTF16Z(data[i:])
		if err != nil {
			return out, err
		}
		i += n
		semi, n, err = readUTF16Z(data[i:])
		if err != nil || semi != ";" {
			return out, fmt.Errorf("expected ';' after value")
		}
		i += n
		if i+4 > len(data) {
			return out, fmt.Errorf("truncated type")
		}
		typ := binary.LittleEndian.Uint32(data[i:])
		i += 4
		semi, n, err = readUTF16Z(data[i:])
		if err != nil || semi != ";" {
			return out, fmt.Errorf("expected ';' after type")
		}
		i += n
		if i+4 > len(data) {
			return out, fmt.Errorf("truncated size")
		}
		sz := binary.LittleEndian.Uint32(data[i:])
		i += 4
		semi, n, err = readUTF16Z(data[i:])
		if err != nil || semi != ";" {
			return out, fmt.Errorf("expected ';' after size")
		}
		i += n
		if i+int(sz) > len(data) {
			return out, fmt.Errorf("truncated data")
		}
		d := append([]byte(nil), data[i:i+int(sz)]...)
		i += int(sz)
		closeB, n, err := readUTF16Z(data[i:])
		if err != nil || closeB != "]" {
			return out, fmt.Errorf("expected ']'")
		}
		i += n
		out = append(out, PolEntry{Key: key, Value: val, Type: typ, Data: d})
	}
	return out, nil
}

func readUTF16Z(b []byte) (string, int, error) {
	if len(b) < 2 {
		return "", 0, fmt.Errorf("truncated utf16")
	}
	var u []uint16
	for i := 0; i+1 < len(b); i += 2 {
		v := binary.LittleEndian.Uint16(b[i:])
		if v == 0 {
			return string(utf16.Decode(u)), i + 2, nil
		}
		u = append(u, v)
	}
	return "", 0, fmt.Errorf("unterminated utf16")
}

func writePolFile(path string, entries []PolEntry) error {
	if err := os.MkdirAll(parentDir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, EncodePol(entries), 0o644)
}

func parentDir(path string) string {
	i := strings.LastIndexAny(path, `/\`)
	if i < 0 {
		return "."
	}
	return path[:i]
}

func readPolFile(path string) ([]PolEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePol(b)
}

func mergePol(existing, add []PolEntry) []PolEntry {
	out := append([]PolEntry(nil), existing...)
	out = append(out, add...)
	return out
}

func filterPol(entries []PolEntry, keep func(PolEntry) bool) []PolEntry {
	var out []PolEntry
	for _, e := range entries {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}
