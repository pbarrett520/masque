package card

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
)

// pngSignature is the 8-byte magic at the start of every PNG.
var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// IsPNG reports whether data begins with the PNG signature.
func IsPNG(data []byte) bool {
	return bytes.HasPrefix(data, pngSignature)
}

type pngChunk struct {
	typ  string
	data []byte
}

// pngChunks iterates the chunk list of a PNG. CRCs are not verified —
// cards from the wild are often re-saved by sloppy tools, and a bad
// checksum on a text chunk shouldn't block an import.
func pngChunks(data []byte) ([]pngChunk, error) {
	if !IsPNG(data) {
		return nil, fmt.Errorf("not a PNG file")
	}
	var chunks []pngChunk
	pos := len(pngSignature)
	for pos+8 <= len(data) {
		length := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		typ := string(data[pos+4 : pos+8])
		end := pos + 8 + length
		if length < 0 || end+4 > len(data) {
			return nil, fmt.Errorf("corrupt PNG: chunk %q overruns file", typ)
		}
		chunks = append(chunks, pngChunk{typ: typ, data: data[pos+8 : end]})
		pos = end + 4 // skip CRC
		if typ == "IEND" {
			break
		}
	}
	if len(chunks) == 0 || chunks[len(chunks)-1].typ != "IEND" {
		return nil, fmt.Errorf("corrupt PNG: missing IEND chunk")
	}
	return chunks, nil
}

// textChunkValue decodes a tEXt or zTXt chunk into (keyword, text).
func textChunkValue(c pngChunk) (string, []byte, error) {
	keyword, rest, ok := bytes.Cut(c.data, []byte{0})
	if !ok {
		return "", nil, fmt.Errorf("malformed %s chunk", c.typ)
	}
	switch c.typ {
	case "tEXt":
		return string(keyword), rest, nil
	case "zTXt":
		if len(rest) < 1 || rest[0] != 0 {
			return "", nil, fmt.Errorf("zTXt chunk with unknown compression method")
		}
		zr, err := zlib.NewReader(bytes.NewReader(rest[1:]))
		if err != nil {
			return "", nil, fmt.Errorf("zTXt chunk: %w", err)
		}
		defer zr.Close() //nolint:errcheck // read-only stream
		text, err := io.ReadAll(io.LimitReader(zr, 32<<20))
		if err != nil {
			return "", nil, fmt.Errorf("zTXt chunk: %w", err)
		}
		return string(keyword), text, nil
	}
	return "", nil, fmt.Errorf("not a text chunk")
}

// pngTextValues extracts all tEXt/zTXt keyword→value pairs from a PNG.
func pngTextValues(data []byte) (map[string][]byte, error) {
	chunks, err := pngChunks(data)
	if err != nil {
		return nil, err
	}
	values := map[string][]byte{}
	for _, c := range chunks {
		if c.typ != "tEXt" && c.typ != "zTXt" {
			continue
		}
		if key, text, err := textChunkValue(c); err == nil {
			values[key] = text
		}
	}
	return values, nil
}

// writeChunk appends one chunk (length, type, data, CRC) to buf.
func writeChunk(buf *bytes.Buffer, typ string, data []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	buf.Write(length[:])
	buf.WriteString(typ)
	buf.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	buf.Write(sum[:])
}

// SetPNGTextChunks returns png with the given tEXt keyword→value pairs
// embedded right after IHDR, replacing any existing tEXt/zTXt chunks
// with the same keywords. A nil value removes the keyword's chunks
// without writing a new one. Used for card export (PNG re-embed,
// spec §7) and fixture generation.
func SetPNGTextChunks(png []byte, values map[string][]byte) ([]byte, error) {
	chunks, err := pngChunks(png)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(pngSignature)
	for i, c := range chunks {
		if c.typ == "tEXt" || c.typ == "zTXt" {
			if key, _, err := textChunkValue(c); err == nil {
				if _, replaced := values[key]; replaced {
					continue
				}
			}
		}
		writeChunk(&out, c.typ, c.data)
		if i == 0 { // IHDR is always first; new chunks go right after
			for _, key := range sortedKeys(values) {
				if values[key] == nil {
					continue // removal only
				}
				writeChunk(&out, "tEXt", append(append([]byte(key), 0), values[key]...))
			}
		}
	}
	return out.Bytes(), nil
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
