package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"golang.org/x/text/encoding/charmap"
	"image"
	"io"
	"log"
	"net"
)

var bo = binary.BigEndian
var pixelFormat = PixelFormat{
	BitsPerPixel: 32,
	BitDepth:     24,
	BigEndian:    true,
	TrueColor:    true,

	RedMax:     255,
	GreenMax:   255,
	BlueMax:    255,
	RedShift:   24,
	GreenShift: 16,
	BlueShift:  8,
}

func main() {
	flag.Parse()

	ln, err := net.Listen("tcp", "127.0.0.1:5900")
	if err != nil {
		log.Fatalf("couldn't listen: %v", err)
	}
	log.Print("listening…")
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatalf("couldn't accept connection: %v", err)
		}
		log.Print("accepted connection")
		go func(conn net.Conn) {
			if err := serve(conn); err != nil {
				log.Printf("serve failed: %v", err)
			}
			if err := conn.Close(); err != nil {
				log.Printf("couldn't close connection: %v", err)
			}
		}(conn)
	}
}

func serve(conn io.ReadWriter) error {
	buf := make([]byte, 256)
	var updateRequest FramebufferUpdateRequest
	var update FramebufferUpdate
	var keyEvent KeyEvent
	var pointerEvent PointerEvent
	const old = true

	log.Print("writing ProtocolVersion…")
	if _, err := io.WriteString(conn, "RFB 003.008\n"); err != nil {
		return fmt.Errorf("couldn't write ProtocolVersion: %v", err)
	}

	log.Print("reading ProtocolVersion…")
	if _, err := io.ReadFull(conn, buf[:12]); err != nil {
		return fmt.Errorf("couldn't read ProtocolVersion: %v", err)
	}
	log.Printf("client requests protocol version %q", buf[:12])

	if old {
		// RFB 3.3
		log.Print("sending authentication scheme…")
		bo.PutUint32(buf, 2) // VNC authentication (macOS won't connect without authentication)
		if _, err := conn.Write(buf[:20]); err != nil {
			return fmt.Errorf("couldn't write authentication scheme: %v", err)
		}

		log.Print("reading challenge response…")
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return fmt.Errorf("couldn't read challenge response: %v", err)
		}

		log.Print("saying authentication succeeded…")
		bo.PutUint32(buf, 0) // OK
		if _, err := conn.Write(buf[:4]); err != nil {
			return fmt.Errorf("couldn't write authentication response: %v", err)
		}
	} else {
		// RFB 3.8
		log.Print("writing potential security types…")
		if _, err := conn.Write([]byte{1, 1}); err != nil {
			return fmt.Errorf("couldn't write security types: %v", err)
		}

		log.Print("reading security type…")
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return fmt.Errorf("couldn't read security type: %v", err)
		}
		if buf[0] != 1 {
			return fmt.Errorf("client must use security type 1, got %q", buf[0])
		}

		log.Print("writing security type…")
		if _, err := conn.Write([]byte{0}); err != nil {
			return fmt.Errorf("couldn't confirm security type: %v", err)
		}
	}

	log.Print("reading ClientInit…")
	if _, err := io.ReadFull(conn, buf[:1]); err != nil {
		return fmt.Errorf("couldn't read ClientInit: %v", err)
	}
	if buf[0] == 0 {
		log.Print("client requests other clients be disconnected")
	} else {
		log.Print("client requests other clients remain connected")
	}

	windowRect := updateUI(image.NewNRGBA(image.ZR), &keyEvent, &pointerEvent)
	if windowRect.Min != image.Pt(0, 0) {
		panic(fmt.Sprintf("window origin must be (0, 0), but it's %v", windowRect.Min))
	}
	bo.PutUint16(buf[0:], uint16(windowRect.Dx())) // width
	bo.PutUint16(buf[2:], uint16(windowRect.Dy())) // height
	pixelFormat.Write(buf[4:])
	bo.PutUint32(buf[20:], 3) // length of name
	copy(buf[24:], "YO!")
	if _, err := conn.Write(buf[:27]); err != nil {
		return fmt.Errorf("couldn't write ServerInit: %v", err)
	}

	for {
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return fmt.Errorf("couldn't read message type: %v", err)
		}
		switch buf[0] {
		case 0: // SetPixelFormat
			if _, err := io.ReadFull(conn, buf[:3+PixelFormatEncodingLength]); err != nil {
				return fmt.Errorf("couldn't read pixel format in SetPixelFormat: %v", err)
			}
			pixelFormat.Read(buf[3:])
			log.Printf("client requested pixel format: %+v", pixelFormat)

		case 2: // SetEncodings
			if _, err := io.ReadFull(conn, buf[:3]); err != nil {
				return fmt.Errorf("couldn't read number of encodings in SetEncodings: %v", err)
			}
			encodingCount := bo.Uint16(buf[1:])
			if int(encodingCount)*4 > len(buf) {
				return fmt.Errorf("can only read %d encodings, but SetEncodings came with %d", len(buf)/4, encodingCount)
			}
			if _, err := io.ReadFull(conn, buf[:4*encodingCount]); err != nil {
				return fmt.Errorf("couldn't read list of encodings in SetEncodings: %v", err)
			}
			requestedEncodings := make([]int32, encodingCount)
			for i := range requestedEncodings {
				requestedEncodings[i] = int32(bo.Uint32(buf[4*i:]))
			}
			log.Printf("client requested one of %d encodings: %v", encodingCount, requestedEncodings)

		case 3: // FramebufferUpdateRequest
			if _, err := io.ReadFull(conn, buf[:FramebufferUpdateRequestEncodingLength]); err != nil {
				return fmt.Errorf("couldn't read FramebufferUpdateRequest: %v", err)
			}
			updateRequest.Read(buf)

			var updateBuf bytes.Buffer
			updateBuf.Write([]byte{0, 0}) // message type and padding
			img := NewPixelFormatImage(pixelFormat, image.Rect(int(updateRequest.X), int(updateRequest.Y), int(updateRequest.X)+int(updateRequest.Width), int(updateRequest.Y)+int(updateRequest.Height)))
			updateUI(img, &keyEvent, &pointerEvent)
			update.Rectangles = []*FramebufferUpdateRect{
				&FramebufferUpdateRect{
					X: updateRequest.X, Y: updateRequest.Y, Width: updateRequest.Width, Height: updateRequest.Height,
					EncodingType: 0, PixelData: img.Pix,
				},
			}
			update.Write(&updateBuf)
			if _, err := updateBuf.WriteTo(conn); err != nil {
				return fmt.Errorf("couldn't write update response: %v", err)
			}

		case 4: // KeyEvent
			if _, err := io.ReadFull(conn, buf[:KeyEventEncodingLength]); err != nil {
				return fmt.Errorf("couldn't read KeyEvent: %v", err)
			}
			keyEvent.Read(buf)
			updateUI(image.NewNRGBA(image.ZR), &keyEvent, &pointerEvent)

		case 5: // PointerEvent
			if _, err := io.ReadFull(conn, buf[:PointerEventEncodingLength]); err != nil {
				return fmt.Errorf("couldn't read PointerEvent: %v", err)
			}
			pointerEvent.Read(buf)
			updateUI(image.NewNRGBA(image.ZR), &keyEvent, &pointerEvent)

		case 6: // ClientCutText
			if _, err := io.ReadFull(conn, buf[:7]); err != nil {
				return fmt.Errorf("couldn't read text length in ClientCutText: %v", err)
			}
			length := bo.Uint32(buf[3:])
			if int(length) > len(buf) {
				return fmt.Errorf("can only read text up to length %d, but ClientCutText came with %d", len(buf), length)
			}
			if _, err := io.ReadFull(conn, buf[:length]); err != nil {
				return fmt.Errorf("couldn't read text in ClientCutText: %v", err)
			}
			converted, err := charmap.ISO8859_1.NewDecoder().Bytes(buf[:length])
			if err != nil {
				return fmt.Errorf("couldn't convert text to UTF-8 in ClientCutText: %v", err)
			}
			text := string(converted)
			log.Printf("client copied text: %q", text)
		}
	}

	return nil
}

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

const PixelFormatEncodingLength = 16

func (pf *PixelFormat) Read(buf []byte) {
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

func (pf *PixelFormat) Write(buf []byte) {
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

type FramebufferUpdateRequest struct {
	Incremental bool
	X           uint16
	Y           uint16
	Width       uint16
	Height      uint16
}

const FramebufferUpdateRequestEncodingLength = 9

func (r *FramebufferUpdateRequest) Read(buf []byte) {
	r.Incremental = buf[0] != 0
	r.X = bo.Uint16(buf[1:])
	r.Y = bo.Uint16(buf[3:])
	r.Width = bo.Uint16(buf[5:])
	r.Height = bo.Uint16(buf[7:])
}

type KeyEvent struct {
	Pressed bool
	KeySym  uint32
}

const KeyEventEncodingLength = 7

func (e *KeyEvent) Read(buf []byte) {
	e.Pressed = buf[0] != 0
	e.KeySym = bo.Uint32(buf[3:])
}

type PointerEvent struct {
	ButtonMask uint8
	X          uint16
	Y          uint16
}

const PointerEventEncodingLength = 5

func (e *PointerEvent) Read(buf []byte) {
	e.ButtonMask = buf[0]
	e.X = bo.Uint16(buf[1:])
	e.Y = bo.Uint16(buf[3:])
}

type FramebufferUpdate struct {
	Rectangles []*FramebufferUpdateRect
}

type FramebufferUpdateRect struct {
	X            uint16
	Y            uint16
	Width        uint16
	Height       uint16
	EncodingType int32
	PixelData    []byte
}

func (u *FramebufferUpdate) Write(buf *bytes.Buffer) {
	binary.Write(buf, bo, uint16(len(u.Rectangles)))
	for _, rect := range u.Rectangles {
		binary.Write(buf, bo, rect.X)
		binary.Write(buf, bo, rect.Y)
		binary.Write(buf, bo, rect.Width)
		binary.Write(buf, bo, rect.Height)
		binary.Write(buf, bo, rect.EncodingType)
		buf.Write(rect.PixelData)
	}
}
