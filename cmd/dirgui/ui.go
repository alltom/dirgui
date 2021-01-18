package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"github.com/alltom/dirgui/rfb"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"image"
	"image/color"
	"image/draw"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const windowWidth = 40 * 8

var (
	primaryColor      = color.NRGBA{0x60, 0x02, 0xee, 0xff}
	primaryLightColor = color.NRGBA{0x99, 0x46, 0xff, 0xff}
)

type Widget struct {
	fileInfo os.FileInfo

	// files
	content string
	editor  EditorState
	loading bool
	saving  bool

	// executables
	running bool

	// files with guis
	guiSize    image.Point
	lastGuiImg image.Image
	guiLock    sync.Mutex

	button1 ButtonState // read for files, run for executables
	button2 ButtonState // save for files
}

type ButtonState struct {
	clicking bool
}

type EditorState struct {
	lastKeySym uint32
}

var wdir string
var widgets []*Widget
var once sync.Once

func getWidgets() {
	switch flag.NArg() {
	case 0:
		wdir = "."
	case 1:
		wdir = flag.Arg(0)
	default:
		log.Fatalf("Expected 0 or 1 arguments, but found %d", flag.NArg())
	}

	infos, err := ioutil.ReadDir(wdir)
	if err != nil {
		log.Fatalf("couldn't read directory %q: %v", wdir, err)
	}

	for _, info := range infos {
		if info.IsDir() {
			continue
		}
		if strings.HasPrefix(info.Name(), ".") {
			continue
		}
		if len(widgets) > 0 && info.Name() == (widgets[len(widgets)-1].fileInfo.Name()+".gui") {
			widget := widgets[len(widgets)-1]

			cmd := &exec.Cmd{
				Path:   info.Name(),
				Args:   []string{info.Name(), widget.fileInfo.Name()},
				Dir:    wdir,
				Stdout: os.Stdout,
				Stderr: os.Stderr,
			}

			imgs := make(chan image.Image)
			bounds, err := nestRfb(cmd, imgs)
			if err != nil {
				log.Fatalf("couldn't launch nested rfb: %v", err)
			}
			widget.guiSize = bounds.Max
			widget.lastGuiImg = image.NewRGBA(image.Rect(0, 0, widget.guiSize.X, widget.guiSize.Y))

			go func(widget *Widget, imgs chan image.Image) {
				for img := range imgs {
					widget.guiLock.Lock()
					widget.lastGuiImg = img
					widget.guiLock.Unlock()
				}
			}(widget, imgs)

			continue
		}
		widgets = append(widgets, &Widget{fileInfo: info})
	}
}

func updateUI(img draw.Image, keyEvent *rfb.KeyEvent, pointerEvent *rfb.PointerEvent) image.Rectangle {
	once.Do(getWidgets)

	var y = 8 // top padding

	// background color
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.ZP, draw.Src)

	for idx, widget := range widgets {
		if widget.guiSize != image.ZP { // has a remote GUI
			label(widget.fileInfo.Name(), image.Rect(8, y, windowWidth-16, y+8), img)
			y += 2 * 8

			widget.guiLock.Lock()
			draw.Draw(img, image.Rect(8, y, 8+widget.guiSize.X, y+widget.guiSize.Y), widget.lastGuiImg, image.ZP, draw.Src)
			widget.guiLock.Unlock()
			y += widget.guiSize.Y + 8
		} else if widget.fileInfo.Mode().Perm()&0111 != 0 { // executable
			label := widget.fileInfo.Name()
			if widget.running {
				label += "..."
			}
			if button(&widget.button1, label, image.Rect(8, y, 30*8, y+3*8), img, pointerEvent) && !widget.running {
				cmd := &exec.Cmd{Path: widget.fileInfo.Name(), Dir: wdir, Stdout: os.Stdout, Stderr: os.Stderr}
				widget.running = true
				go func(widget *Widget, cmd *exec.Cmd) {
					if err := cmd.Run(); err != nil {
						log.Printf("exec failed: %v", err)
					}
					widget.running = false
				}(widget, cmd)
			}
			y += 3 * 8
		} else { // not executable
			label(widget.fileInfo.Name(), image.Rect(8, y, windowWidth-16, y+8), img)
			y += 2 * 8

			x := 8

			edit(&widget.editor, &widget.content, image.Rect(x, y, x+22*8, y+3*8), img, keyEvent, pointerEvent)
			x += 23 * 8

			label := "Load"
			if widget.loading {
				label += "..."
			}
			if button(&widget.button1, "Load", image.Rect(x, y, x+7*8, y+3*8), img, pointerEvent) && !widget.loading && !widget.saving {
				widget.loading = true
				go func(widget *Widget) {
					path := filepath.Join(wdir, widget.fileInfo.Name())
					f, err := os.Open(path)
					if err != nil {
						log.Printf("couldn't open %q: %v", path, err)
						widget.loading = false
						return
					}
					defer f.Close()

					scanner := bufio.NewScanner(f)
					if scanner.Scan() {
						widget.content = scanner.Text()
					}

					if err = scanner.Err(); err != nil {
						log.Printf("couldn't read %q: %v", path, err)
					}
					widget.loading = false
				}(widget)
			}
			x += 8 * 8

			label = "Save"
			if widget.saving {
				label += "..."
			}
			if button(&widget.button2, label, image.Rect(x, y, x+7*8, y+3*8), img, pointerEvent) && !widget.loading && !widget.saving {
				widget.saving = true
				go func(widget *Widget, content string) {
					path := filepath.Join(wdir, widget.fileInfo.Name())
					if err := ioutil.WriteFile(path, []byte(content), 0666); err != nil {
						log.Printf("couldn't write %q: %v", path, err)
					}
					widget.saving = false
				}(widget, widget.content)
			}

			y += 3 * 8
		}

		y += 8
		if idx < len(widgets)-1 {
			y += 8
		}
	}

	return image.Rect(0, 0, windowWidth, y)
}

func label(text string, rect image.Rectangle, img draw.Image) {
	fd := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.Black),
		Face: basicfont.Face7x13,
		Dot:  fixed.Point26_6{fixed.I(rect.Min.X), fixed.I(rect.Max.Y)},
	}
	fd.DrawString(text)
}

func button(state *ButtonState, text string, rect image.Rectangle, img draw.Image, pointerEvent *rfb.PointerEvent) bool {
	hovering := image.Pt(int(pointerEvent.X), int(pointerEvent.Y)).In(rect)
	buttonDown := pointerEvent.ButtonMask&1 != 0

	// TODO: Require that the click started on the button.
	var clicked bool
	if state.clicking {
		if !buttonDown {
			clicked = hovering
			state.clicking = false
		}
	} else {
		if hovering && buttonDown {
			state.clicking = true
		}
	}

	c := image.Uniform{primaryColor}
	if hovering {
		if buttonDown {
			c.C = color.Black
		} else {
			c.C = primaryLightColor
		}
	}
	draw.Draw(img, rect, &c, image.ZP, draw.Src)

	fd := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.White),
		Face: basicfont.Face7x13,
		Dot:  fixed.Point26_6{fixed.I(rect.Min.X + 8), fixed.I(rect.Max.Y - 8)},
	}
	fd.DrawString(text)

	return clicked
}

func edit(state *EditorState, text *string, rect image.Rectangle, img draw.Image, keyEvent *rfb.KeyEvent, pointerEvent *rfb.PointerEvent) {
	draw.Draw(img, rect, image.NewUniform(color.Black), image.ZP, draw.Src)
	draw.Draw(img, rect.Inset(1), image.NewUniform(color.White), image.ZP, draw.Src)

	hovering := image.Pt(int(pointerEvent.X), int(pointerEvent.Y)).In(rect)
	if hovering {
		if keyEvent.Pressed {
			if state.lastKeySym != keyEvent.KeySym {
				if keyEvent.KeySym >= 32 && keyEvent.KeySym <= 126 {
					*text += string([]uint8{uint8(keyEvent.KeySym)})
				} else if keyEvent.KeySym == 0xff08 {
					*text = (*text)[:len(*text)-1]
				}
			}
			state.lastKeySym = keyEvent.KeySym
		} else {
			state.lastKeySym = 0
		}
	}

	fd := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.Black),
		Face: basicfont.Face7x13,
		Dot:  fixed.Point26_6{fixed.I(rect.Min.X + 8), fixed.I(rect.Max.Y - 8)},
	}
	fd.DrawString(*text)
}

func nestRfb(cmd *exec.Cmd, imgs chan image.Image) (image.Rectangle, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:")
	if err != nil {
		return image.ZR, fmt.Errorf("couldn't listen: %v", err)
	}

	log.Printf("starting subprocess at %s…", ln.Addr().String())
	cmd.Args = append([]string{cmd.Args[0], "--parent_vnc_addr", ln.Addr().String()}, cmd.Args[1:]...)
	if err := cmd.Start(); err != nil {
		return image.ZR, fmt.Errorf("couldn't start subprocess: %v", err)
	}

	log.Print("waiting for subprocess connection…")
	conn, err := ln.Accept()
	if err != nil {
		log.Fatalf("couldn't accept connection: %v", err)
	}

	bounds := make(chan image.Rectangle, 1)
	boundsCallback := func(rect image.Rectangle) {
		bounds <- rect
	}
	imageCallback := func(img *rfb.PixelFormatImage) {
		imgs <- img
	}

	go func() {
		defer cmd.Process.Kill()

		log.Print("starting VNC client for subprocess…")
		if err := rfbClient(conn, boundsCallback, imageCallback); err != nil {
			log.Printf("[rfbClient] client failed: %v", err)
		}
		if err := conn.Close(); err != nil {
			log.Printf("couldn't close connection: %v", err)
		}
	}()

	return <-bounds, nil
}

// rfbClient communicates over conn as an RFB 3.3 client and calls callback with the composite framebuffer after each update, then requests another update. callback must not retain the image after it returns.
func rfbClient(conn io.ReadWriter, boundsCallback func(image.Rectangle), callback func(*rfb.PixelFormatImage)) error {
	buf := make([]byte, 256)

	var bo = binary.BigEndian
	var width, height uint16
	var pixelFormat rfb.PixelFormat
	var framebuffer *rfb.PixelFormatImage
	var updateRequest rfb.FramebufferUpdateRequest

	if _, err := io.ReadFull(conn, buf[:12]); err != nil {
		return fmt.Errorf("couldn't read ProtocolVersion: %v", err)
	}
	// Disregard, since every server should support 3.3

	if _, err := io.WriteString(conn, "RFB 003.003\n"); err != nil {
		return fmt.Errorf("couldn't write ProtocolVersion: %v", err)
	}

	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return fmt.Errorf("couldn't read authentication scheme: %v", err)
	}
	authenticationScheme := bo.Uint32(buf[:4])
	if authenticationScheme != 1 {
		return fmt.Errorf("authentication is not supported, but server requested scheme %d", authenticationScheme)
	}

	// Send ClientInitialization
	buf[0] = 1 // Share desktop with other clients
	if _, err := conn.Write(buf[:1]); err != nil {
		return fmt.Errorf("couldn't write ClientInitialization: %v", err)
	}

	if _, err := io.ReadFull(conn, buf[:4+rfb.PixelFormatEncodingLength+4]); err != nil {
		return fmt.Errorf("couldn't read ServerInitialization: %v", err)
	}
	width = bo.Uint16(buf[0:])
	height = bo.Uint16(buf[2:])
	pixelFormat.Read(buf[4:], bo)
	nameLength := bo.Uint32(buf[4+rfb.PixelFormatEncodingLength:])
	if nameLength > uint32(len(buf)) {
		return fmt.Errorf("server name must be less than %d bytes, but it's %d bytes", len(buf), nameLength)
	}
	if _, err := io.ReadFull(conn, buf[:nameLength]); err != nil {
		return fmt.Errorf("couldn't read server name: %v", err)
	}

	boundsCallback(image.Rect(0, 0, int(width), int(height)))
	framebuffer = rfb.NewPixelFormatImage(pixelFormat, image.Rect(0, 0, int(width), int(height)))
	updateRequest = rfb.FramebufferUpdateRequest{
		Incremental: true,
		X:           0, Y: 0,
		Width: width, Height: height,
	}

	buf[0] = 3 // FramebufferUpdateRequest
	updateRequest.Write(buf[1:], bo)
	if _, err := conn.Write(buf[:1+rfb.FramebufferUpdateRequestEncodingLength]); err != nil {
		return fmt.Errorf("couldn't write FramebufferUpdateRequest: %v", err)
	}

	for {
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return fmt.Errorf("couldn't read message type: %v", err)
		}
		switch buf[0] {
		case 0: // FramebufferUpdate
			if _, err := io.ReadFull(conn, buf[:3]); err != nil {
				return fmt.Errorf("couldn't read FramebufferUpdate: %v", err)
			}
			rectangleCount := bo.Uint16(buf[1:])
			for i := uint16(0); i < rectangleCount; i++ {
				var rect rfb.FramebufferUpdateRect
				if err := rect.Read(conn, bo, pixelFormat); err != nil {
					return fmt.Errorf("couldn't read rectangle %d: %v", i, err)
				}
				img := &rfb.PixelFormatImage{
					Pix:         rect.PixelData,
					Rect:        image.Rect(int(rect.X), int(rect.Y), int(rect.X)+int(rect.Width), int(rect.Y)+int(rect.Height)),
					PixelFormat: pixelFormat,
				}
				draw.Draw(framebuffer, framebuffer.Bounds(), img, image.ZP, draw.Src)
				callback(framebuffer)

				buf[0] = 3 // FramebufferUpdateRequest
				updateRequest.Write(buf[1:], bo)
				if _, err := conn.Write(buf[:1+rfb.FramebufferUpdateRequestEncodingLength]); err != nil {
					return fmt.Errorf("couldn't write FramebufferUpdateRequest: %v", err)
				}
			}

		case 1: // SetColourMapEntries
			return fmt.Errorf("SetColourMapEntries is not supported")

		case 2: // Bell
			// Whee!

		case 3: // ServerCutText
			if _, err := io.ReadFull(conn, buf[:7]); err != nil {
				return fmt.Errorf("couldn't read ServerCutText: %v", err)
			}
			length := bo.Uint32(buf[3:])
			// Not supported, so throw it away.
			io.Copy(ioutil.Discard, &io.LimitedReader{R: conn, N: int64(length)})

		default:
			return fmt.Errorf("received unrecognized message %d", buf[0])
		}

	}

	return nil
}
