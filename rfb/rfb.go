package rfb

import (
	"encoding/binary"
	"fmt"
	"io"
)

type PixelFormat struct {
	BitsPerPixel uint8
	BitDepth     uint8
	BigEndian    bool
	TrueColor    bool

	RedMax     uint16
	GreenMax   uint16
	BlueMax    uint16
	RedShift   uint8
	GreenShift uint8
	BlueShift  uint8
}

type FramebufferUpdateRequest struct {
	Incremental bool
	X           uint16
	Y           uint16
	Width       uint16
	Height      uint16
}

type KeyEvent struct {
	Pressed bool
	KeySym  uint32
}

type PointerEvent struct {
	ButtonMask uint8
	X          uint16
	Y          uint16
}

type FramebufferUpdate struct {
	Rectangles []*FramebufferUpdateRect
}

type FramebufferUpdateRect struct {
	X            uint16
	Y            uint16
	Width        uint16
	Height       uint16
	EncodingType uint32 // Unsigned per spec, but often interpreted signed
	PixelData    []byte
}

const (
	PixelFormatEncodingLength              = 16
	FramebufferUpdateRequestEncodingLength = 9
	KeyEventEncodingLength                 = 7
	PointerEventEncodingLength             = 5
)

// buf must contain at least PixelFormatEncodingLength bytes.
func (pf *PixelFormat) Read(buf []byte, bo binary.ByteOrder) {
	pf.BitsPerPixel = buf[0]
	pf.BitDepth = buf[1]
	pf.BigEndian = buf[2] != 0
	pf.TrueColor = buf[3] != 0

	pf.RedMax = bo.Uint16(buf[4:])
	pf.GreenMax = bo.Uint16(buf[6:])
	pf.BlueMax = bo.Uint16(buf[8:])
	pf.RedShift = buf[10]
	pf.GreenShift = buf[11]
	pf.BlueShift = buf[12]
}

// buf must contain at least PixelFormatEncodingLength bytes.
func (pf *PixelFormat) Write(buf []byte, bo binary.ByteOrder) {
	buf[0] = pf.BitsPerPixel
	buf[1] = pf.BitDepth
	if pf.BigEndian {
		buf[2] = 1
	} else {
		buf[2] = 0
	}
	if pf.TrueColor {
		buf[3] = 1
	} else {
		buf[3] = 0
	}
	bo.PutUint16(buf[4:], pf.RedMax)
	bo.PutUint16(buf[6:], pf.GreenMax)
	bo.PutUint16(buf[8:], pf.BlueMax)
	buf[10] = pf.RedShift
	buf[11] = pf.GreenShift
	buf[12] = pf.BlueShift
}

// buf must contain at least FramebufferUpdateRequestEncodingLength bytes.
func (r *FramebufferUpdateRequest) Read(buf []byte, bo binary.ByteOrder) {
	r.Incremental = buf[0] != 0
	r.X = bo.Uint16(buf[1:])
	r.Y = bo.Uint16(buf[3:])
	r.Width = bo.Uint16(buf[5:])
	r.Height = bo.Uint16(buf[7:])
}

// buf must contain at least FramebufferUpdateRequestEncodingLength bytes.
func (r *FramebufferUpdateRequest) Write(buf []byte, bo binary.ByteOrder) {
	if r.Incremental {
		buf[0] = 1
	} else {
		buf[0] = 0
	}
	bo.PutUint16(buf[1:], r.X)
	bo.PutUint16(buf[3:], r.Y)
	bo.PutUint16(buf[5:], r.Width)
	bo.PutUint16(buf[7:], r.Height)
}

// buf must contain at least KeyEventEncodingLength bytes.
func (e *KeyEvent) Read(buf []byte, bo binary.ByteOrder) {
	e.Pressed = buf[0] != 0
	e.KeySym = bo.Uint32(buf[3:])
}

// buf must contain at least PointerEventEncodingLength bytes.
func (e *PointerEvent) Read(buf []byte, bo binary.ByteOrder) {
	e.ButtonMask = buf[0]
	e.X = bo.Uint16(buf[1:])
	e.Y = bo.Uint16(buf[3:])
}

func (rect *FramebufferUpdateRect) Read(r io.Reader, bo binary.ByteOrder, pixelFormat PixelFormat) error {
	var buf [12]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return err
	}
	rect.X = bo.Uint16(buf[0:])
	rect.Y = bo.Uint16(buf[2:])
	rect.Width = bo.Uint16(buf[4:])
	rect.Height = bo.Uint16(buf[6:])
	rect.EncodingType = bo.Uint32(buf[8:])
	if rect.EncodingType != 0 {
		return fmt.Errorf("only raw encoding is supported, but it is %d", rect.EncodingType)
	}
	rect.PixelData = make([]byte, int(pixelFormat.BitsPerPixel/8)*int(rect.Width)*int(rect.Height))
	if _, err := io.ReadFull(r, rect.PixelData); err != nil {
		return err
	}
	return nil
}

func (u *FramebufferUpdate) Write(w io.Writer, bo binary.ByteOrder) error {
	if err := binary.Write(w, bo, uint16(len(u.Rectangles))); err != nil {
		return err
	}
	for _, rect := range u.Rectangles {
		var buf [12]byte
		bo.PutUint16(buf[0:], rect.X)
		bo.PutUint16(buf[2:], rect.Y)
		bo.PutUint16(buf[4:], rect.Width)
		bo.PutUint16(buf[6:], rect.Height)
		bo.PutUint32(buf[8:], uint32(rect.EncodingType))
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
		if _, err := w.Write(rect.PixelData); err != nil {
			return err
		}
	}
	return nil
}
