package main

import (
	"encoding/binary"
	"unsafe"
)

func GetCommand(id int, command []byte) []byte {
	cmd := []byte{'0', byte(id + '0')}
	cmd = append(cmd, command...)
	cs := crc16(cmd)
	cmd = append(cmd, cs...)
	cmd = append(cmd, '\r')
	return cmd
}

func crc16(data []byte) []byte {
	cs := uint16(0)
	for _, b := range data {
		cs ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			carry := cs & 0x8000
			if carry != 0 {
				cs ^= 0x8810
			}
			cs = (cs << 1) + (carry >> 15)
		}
	}
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, cs)
	// Always use big-endian order for CRC
	return buf
}

func isLittleEndian() bool {
	var i uint16 = 1
	b := (*[2]byte)(unsafe.Pointer(&i))
	return b[0] == 1
}
