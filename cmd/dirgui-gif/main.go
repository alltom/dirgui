package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"github.com/alltom/dirgui/rfb"
	"image"
	"image/draw"
	"image/gif"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"time"
)

var vncAddr = flag.String("parent_vnc_addr", "", "If present, instead of starting a VNC server, will connect to the given addr as a VNC server")

func main() {
	flag.Parse()

	if flag.NArg() != 1 {
		log.Fatalf("expected one argument, the path to a GIF, but found %q", flag.Args())
	}

	g, err := loadGif(flag.Arg(0))
	if err != nil {
		log.Fatalf("couldn't load gif: %v", err)
	}

	bounds := g.Image[0].Bounds()
	for _, img := range g.Image {
		bounds = bounds.Union(img.Bounds())
	}

	var frames []image.Image
	accum := image.NewNRGBA(bounds)
	draw.Draw(accum, accum.Bounds(), g.Image[0], image.ZP, draw.Src)
	for _, img := range g.Image {
		draw.Draw(accum, accum.Bounds(), img, image.ZP, draw.Over)

		frame := image.NewNRGBA(bounds)
		draw.Draw(frame, frame.Bounds(), accum, image.ZP, draw.Src)
		frames = append(frames, frame)
	}

	imgs := make(chan image.Image)

	go func() {
		for i := 0; ; i = (i + 1) % len(frames) {
			imgs <- frames[i]
			time.Sleep(time.Millisecond * time.Duration(g.Delay[i]*10))
		}
	}()

	if *vncAddr == "" {
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
				if err := rfbServe(conn, bounds, imgs); err != nil {
					log.Printf("serve failed: %v", err)
				}
				if err := conn.Close(); err != nil {
					log.Printf("couldn't close connection: %v", err)
				}
			}(conn)
		}
	} else {
		conn, err := net.Dial("tcp", *vncAddr)
		if err != nil {
			log.Fatalf("couldn't connect to %q: %v", *vncAddr, err)
		}

		if err := rfbServe(conn, bounds, imgs); err != nil {
			log.Printf("serve failed: %v", err)
		}
		if err := conn.Close(); err != nil {
			log.Printf("couldn't close connection: %v", err)
		}
	}
}

func loadGif(path string) (*gif.GIF, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("couldn't open %q: %v", path, err)
	}
	defer f.Close()

	return gif.DecodeAll(f)
}

func rfbServe(conn io.ReadWriter, rect image.Rectangle, imgs <-chan image.Image) error {
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
		bo.PutUint32(buf, 1)
		if _, err := conn.Write(buf[:4]); err != nil {
			return fmt.Errorf("couldn't write authentication scheme: %v", err)
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

	if _, err := io.ReadFull(conn, buf[:1]); err != nil {
		return fmt.Errorf("couldn't read ClientInit: %v", err)
	}
	if buf[0] == 0 {
		log.Print("client requests other clients be disconnected")
	} else {
		log.Print("client requests other clients remain connected")
	}

	bo.PutUint16(buf[0:], uint16(rect.Max.X))
	bo.PutUint16(buf[2:], uint16(rect.Max.Y))
	pixelFormat.Write(buf[4:], bo)
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
			if _, err := io.ReadFull(conn, buf[:3+rfb.PixelFormatEncodingLength]); err != nil {
				return fmt.Errorf("couldn't read pixel format in SetPixelFormat: %v", err)
			}
			pixelFormat.Read(buf[3:], bo)

		case 2: // SetEncodings
			if _, err := io.ReadFull(conn, buf[:3]); err != nil {
				return fmt.Errorf("couldn't read number of encodings in SetEncodings: %v", err)
			}
			encodingCount := bo.Uint16(buf[1:])
			if _, err := io.Copy(ioutil.Discard, &io.LimitedReader{R: conn, N: 4 * int64(encodingCount)}); err != nil {
				return fmt.Errorf("couldn't read SetEncodings encoding list: %v", err)
			}

		case 3: // FramebufferUpdateRequest
			if _, err := io.ReadFull(conn, buf[:rfb.FramebufferUpdateRequestEncodingLength]); err != nil {
				return fmt.Errorf("couldn't read FramebufferUpdateRequest: %v", err)
			}
			updateRequest.Read(buf, bo)

			img := rfb.NewPixelFormatImage(pixelFormat, image.Rect(int(updateRequest.X), int(updateRequest.Y), int(updateRequest.X)+int(updateRequest.Width), int(updateRequest.Y)+int(updateRequest.Height)))
			draw.Draw(img, img.Bounds(), <-imgs, image.ZP, draw.Src)
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
			if _, err := io.Copy(ioutil.Discard, &io.LimitedReader{R: conn, N: rfb.KeyEventEncodingLength}); err != nil {
				return fmt.Errorf("couldn't read KeyEvent: %v", err)
			}

		case 5: // PointerEvent
			if _, err := io.Copy(ioutil.Discard, &io.LimitedReader{R: conn, N: rfb.PointerEventEncodingLength}); err != nil {
				return fmt.Errorf("couldn't read PointerEvent: %v", err)
			}

		case 6: // ClientCutText
			if _, err := io.ReadFull(conn, buf[:7]); err != nil {
				return fmt.Errorf("couldn't read text length in ClientCutText: %v", err)
			}
			length := bo.Uint32(buf[3:])
			if _, err := io.Copy(ioutil.Discard, &io.LimitedReader{R: conn, N: int64(length)}); err != nil {
				return fmt.Errorf("couldn't read ClientCutText text: %v", err)
			}

		default:
			return fmt.Errorf("received unrecognized message %d", buf[0])
		}
	}

	return nil
}
