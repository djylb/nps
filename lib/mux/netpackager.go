package mux

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/djylb/nps/lib/logs"
)

type basePackager struct {
	buf []byte
	// buf contain the mux protocol struct binary data, we copy data to buf firstly.
	// replace binary.Read/Write method, it may use reflect shows slowly.
	// also reduce Conn.Read/Write calls which use syscall.
	// due to our test, Conn.Write method reduce by two-thirds CPU times,
	// Conn.Write method has 20% reduction of the CPU times,
	// totally provides more than twice of the CPU performance improvement.
	length  uint16
	content []byte
}

func (Self *basePackager) Set(content []byte) (err error) {
	Self.reset()

	if content == nil {
		logs.Error("mux:packer: new pack content is nil")
		return
		//panic("mux:packer: new pack content is nil")
		//err = errors.New("mux:packer: new pack content is nil")
	}

	n := len(content)
	//fmt.Println(content)
	//if n == 0 {
	//	// 长度为0的包，不应该向上抛，不然客户端会EOF，这里暂时没解决空包的问题 TODO
	//	//logs.Error("mux:packer: new pack content is zero length")
	//	//err = errors.New("mux:packer: new pack content is zero length")
	//}
	if n > maximumSegmentSize {
		logs.Error("mux:packer: new pack content segment too large")
		//err = errors.New("mux:packer: new pack content segment too large")
		return
	}

	if Self.content == nil {
		if cap(Self.buf) < 7+n {
			logs.Error("mux:packer: buf too small")
			return
		}
		copy(Self.buf[7:7+n], content)
	} else {
		if cap(Self.content) < n {
			logs.Error("mux:packer: buf too small")
			return
		}
		copy(Self.content[:n], content)
	}
	Self.length = uint16(n)
	return
}

func (Self *basePackager) GetContent() (content []byte, err error) {
	if Self.length == 0 || (Self.content == nil && Self.buf == nil) {
		return nil, errors.New("mux:packer:content is nil")
	}
	if Self.content == nil {
		return Self.buf[7 : 7+Self.length], nil
	}
	return Self.content[:Self.length], nil
}

func (Self *basePackager) Pack(writer io.Writer) (err error) {
	binary.LittleEndian.PutUint16(Self.buf[5:7], Self.length)
	if Self.content == nil {
		_, err = writer.Write(Self.buf[:7+Self.length])
	} else {
		_, err = writer.Write(Self.buf[:7])
		if err != nil {
			return
		}
		_, err = writer.Write(Self.content[:Self.length])
	}
	return
}

func (Self *basePackager) UnPack(reader io.Reader) (n uint16, err error) {
	Self.reset()
	l, err := io.ReadFull(reader, Self.buf[5:7])
	if err != nil {
		return
	}
	n += uint16(l)
	Self.length = binary.LittleEndian.Uint16(Self.buf[5:7])

	if int(Self.length) > maximumSegmentSize {
		err = errors.New("mux:packer: unpack content segment too large")
		return
	}

	if Self.content == nil {
		if cap(Self.buf) < 7+int(Self.length) {
			err = errors.New("mux:packer: unpack err, content length too large")
			return
		}
		l, err = io.ReadFull(reader, Self.buf[7:7+Self.length])
	} else {
		if int(Self.length) > cap(Self.content) {
			err = errors.New("mux:packer: unpack err, content length too large")
			return
		}
		l, err = io.ReadFull(reader, Self.content[:Self.length])
	}

	n += uint16(l)
	return
}

func (Self *basePackager) reset() {
	Self.length = 0
	//Self.content = nil
	//Self.buf = nil
}

type muxPackager struct {
	flag     uint8
	id       int32
	window   uint64
	priority bool
	basePackager
}

func (Self *muxPackager) Set(flag uint8, id int32, content interface{}) (err error) {
	Self.buf = windowBuff.Get()
	Self.flag = flag
	Self.id = id
	switch flag {
	case muxPingFlag, muxPingReturn, muxNewMsg, muxNewMsgPart:
		//Self.content = windowBuff.Get()
		if content != nil {
			err = Self.basePackager.Set(content.([]byte))
		}
	case muxMsgSendOk:
		// MUX_MSG_SEND_OK contains one data
		Self.window = content.(uint64)
	default:
	}
	return
}

func (Self *muxPackager) Pack(writer io.Writer) (err error) {
	//Self.buf = Self.buf[0:13]
	Self.buf[0] = Self.flag
	binary.LittleEndian.PutUint32(Self.buf[1:5], uint32(Self.id))
	switch Self.flag {
	case muxNewMsg, muxNewMsgPart, muxPingFlag, muxPingReturn:
		err = Self.basePackager.Pack(writer)
		if Self.content != nil {
			windowBuff.Put(Self.content)
			Self.content = nil
		}
	case muxMsgSendOk:
		binary.LittleEndian.PutUint64(Self.buf[5:13], Self.window)
		_, err = writer.Write(Self.buf[:13])
	default:
		_, err = writer.Write(Self.buf[:5])
	}
	windowBuff.Put(Self.buf)
	Self.buf = nil
	return
}

func (Self *muxPackager) UnPack(reader io.Reader) (n uint16, err error) {
	Self.buf = windowBuff.Get()
	//Self.buf = Self.buf[0:13]
	l, err := io.ReadFull(reader, Self.buf[:5])
	if err != nil {
		windowBuff.Put(Self.buf)
		Self.buf = nil
		return
	}
	n += uint16(l)
	Self.flag = Self.buf[0]
	Self.id = int32(binary.LittleEndian.Uint32(Self.buf[1:5]))
	switch Self.flag {
	case muxNewMsg, muxNewMsgPart, muxPingFlag, muxPingReturn:
		var m uint16
		Self.content = windowBuff.Get() // need Get a window buf from pool
		m, err = Self.basePackager.UnPack(reader)
		n += m
	case muxMsgSendOk:
		l, err = io.ReadFull(reader, Self.buf[5:13])
		if err == nil {
			Self.window = binary.LittleEndian.Uint64(Self.buf[5:13])
			n += uint16(l) // uint64
		}
	default:
	}
	windowBuff.Put(Self.buf)
	Self.buf = nil
	return
}

func (Self *muxPackager) reset() {
	Self.id = 0
	Self.flag = 0
	Self.length = 0
	Self.content = nil
	Self.window = 0
	if Self.buf != nil {
		windowBuff.Put(Self.buf)
	}
	Self.buf = nil
}
