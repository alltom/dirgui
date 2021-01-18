package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"github.com/alltom/dirgui/rfb"
	"golang.org/x/text/encoding/charmap"
	"image"
	"io"
	"log"
	"net"
)

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
			if err := rfbServe(conn); err != nil {
				log.Printf("serve failed: %v", err)
			}
			if err := conn.Close(); err != nil {
				log.Printf("couldn't close connection: %v", err)
			}
		}(conn)
	}
}

func rfbServe(conn io.ReadWriter) error {
	buf := make([]byte, 256)
	w := bufio.NewWriter(conn)

	var bo = binary.BigEndian
	var pixelFormat = rfb.PixelFormat{
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
	var updateRequest rfb.FramebufferUpdateRequest
	var update rfb.FramebufferUpdate
	var keyEvent rfb.KeyEvent
	var pointerEvent rfb.PointerEvent

	if _, err := io.WriteString(conn, "RFB 003.008\n"); err != nil {
		return fmt.Errorf("couldn't write ProtocolVersion: %v", err)
	}

	var major, minor int
	if _, err := io.ReadFull(conn, buf[:12]); err != nil {
		return fmt.Errorf("couldn't read ProtocolVersion: %v", err)
	}
	if _, err := fmt.Sscanf(string(buf[:12]), "RFB %03d.%03d\n", &major, &minor); err != nil {
		return fmt.Errorf("couldn't parse ProtocolVersion %q: %v", buf[:12], err)
	}

	if major == 3 && minor == 3 {
		// RFB 3.3
		bo.PutUint32(buf, 2) // VNC authentication (macOS won't connect without authentication)
		if _, err := conn.Write(buf[:20]); err != nil {
			return fmt.Errorf("couldn't write authentication scheme: %v", err)
		}

		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return fmt.Errorf("couldn't read challenge response: %v", err)
		}

		bo.PutUint32(buf, 0) // OK
		if _, err := conn.Write(buf[:4]); err != nil {
			return fmt.Errorf("couldn't write authentication response: %v", err)
		}
	} else if major == 3 && minor == 8 {
		// RFB 3.8
		if _, err := conn.Write([]byte{1, 1}); err != nil {
			return fmt.Errorf("couldn't write security types: %v", err)
		}

		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return fmt.Errorf("couldn't read security type: %v", err)
		}
		if buf[0] != 1 {
			return fmt.Errorf("client must use security type 1, got %q", buf[0])
		}

		if _, err := conn.Write([]byte{0}); err != nil {
			return fmt.Errorf("couldn't confirm security type: %v", err)
		}
	} else {
		return fmt.Errorf("server only supports RFB 3.3 and 3.8, but client requested %d.%d", major, minor)
	}

	log.Print("reading ClientInit…")
	if _, err := io.ReadFull(conn, buf[:1]); err != nil {
		return fmt.Errorf("couldn't read ClientInit: %v", err)
	}

	windowRect := updateUI(image.NewNRGBA(image.ZR), &keyEvent, &pointerEvent)
	if windowRect.Min != image.Pt(0, 0) {
		panic(fmt.Sprintf("window origin must be (0, 0), but it's %v", windowRect.Min))
	}
	bo.PutUint16(buf[0:], uint16(windowRect.Dx())) // width
	bo.PutUint16(buf[2:], uint16(windowRect.Dy())) // height
	pixelFormat.Write(buf[4:], bo)
	bo.PutUint32(buf[20:], 6) // length of name
	copy(buf[24:], "dirgui")
	if _, err := conn.Write(buf[:30]); err != nil {
		return fmt.Errorf("couldn't write ServerInit: %v", err)
	}

	for {
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return fmt.Errorf("couldn't read message type: %v", err)
		}
		switch buf[0] {
		case 0: // SetPixelFormat
			if _, err := io.ReadFull(conn, buf[:3+rfb.PixelFormatEncodingLength]); err != nil {
				return fmt.Errorf("couldn't read pixel format in SetPixelFormat: %v", err)
			}
			pixelFormat.Read(buf[3:], bo)

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
			if _, err := io.ReadFull(conn, buf[:rfb.FramebufferUpdateRequestEncodingLength]); err != nil {
				return fmt.Errorf("couldn't read FramebufferUpdateRequest: %v", err)
			}
			updateRequest.Read(buf, bo)

			img := rfb.NewPixelFormatImage(pixelFormat, image.Rect(int(updateRequest.X), int(updateRequest.Y), int(updateRequest.X)+int(updateRequest.Width), int(updateRequest.Y)+int(updateRequest.Height)))
			updateUI(img, &keyEvent, &pointerEvent)
			update.Rectangles = []*rfb.FramebufferUpdateRect{
				&rfb.FramebufferUpdateRect{
					X: updateRequest.X, Y: updateRequest.Y, Width: updateRequest.Width, Height: updateRequest.Height,
					EncodingType: 0, PixelData: img.Pix,
				},
			}

			if _, err := w.Write([]byte{0, 0}); err != nil { // message type and padding
				return fmt.Errorf("couldn't write FramebufferUpdate header: %v", err)
			}
			if err := update.Write(w, bo); err != nil {
				return fmt.Errorf("couldn't write FramebufferUpdate: %v", err)
			}
			if err := w.Flush(); err != nil {
				return fmt.Errorf("couldn't write FramebufferUpdate: %v", err)
			}

		case 4: // KeyEvent
			if _, err := io.ReadFull(conn, buf[:rfb.KeyEventEncodingLength]); err != nil {
				return fmt.Errorf("couldn't read KeyEvent: %v", err)
			}
			keyEvent.Read(buf, bo)
			updateUI(image.NewNRGBA(image.ZR), &keyEvent, &pointerEvent)

		case 5: // PointerEvent
			if _, err := io.ReadFull(conn, buf[:rfb.PointerEventEncodingLength]); err != nil {
				return fmt.Errorf("couldn't read PointerEvent: %v", err)
			}
			pointerEvent.Read(buf, bo)
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

		default:
			return fmt.Errorf("received unrecognized message %d", buf[0])
		}
	}

	return nil
}
