package pngmeta

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// EmbedITXt inserts an iTXt chunk with the given keyword and UTF-8 text
// into a PNG byte slice, just before the IEND chunk.
func EmbedITXt(pngData []byte, keyword, text string) ([]byte, error) {
	// IEND is always the last chunk: 4-byte zero length + "IEND" + 4-byte CRC
	iendMarker := []byte("IEND")
	idx := bytes.LastIndex(pngData, iendMarker)
	if idx < 4 {
		return nil, fmt.Errorf("IEND chunk not found in PNG data")
	}
	insertAt := idx - 4 // back up over the length field

	chunk := makeITXtChunk(keyword, text)

	out := make([]byte, 0, len(pngData)+len(chunk))
	out = append(out, pngData[:insertAt]...)
	out = append(out, chunk...)
	out = append(out, pngData[insertAt:]...)
	return out, nil
}

func makeITXtChunk(keyword, text string) []byte {
	var payload bytes.Buffer
	payload.WriteString(keyword)
	payload.WriteByte(0) // null separator after keyword
	payload.WriteByte(0) // compression flag: uncompressed
	payload.WriteByte(0) // compression method
	payload.WriteByte(0) // language tag: empty
	payload.WriteByte(0) // translated keyword: empty
	payload.WriteString(text)

	chunkType := []byte("iTXt")
	data := payload.Bytes()

	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))

	crcInput := append(chunkType, data...)
	crcVal := make([]byte, 4)
	binary.BigEndian.PutUint32(crcVal, crc32.ChecksumIEEE(crcInput))

	var chunk bytes.Buffer
	chunk.Write(length)
	chunk.Write(chunkType)
	chunk.Write(data)
	chunk.Write(crcVal)
	return chunk.Bytes()
}
