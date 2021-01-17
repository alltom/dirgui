package main

import (
	"bufio"
	"flag"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"image"
	"image/color"
	"image/draw"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
	switch len(flag.Args()) {
	case 0:
		wdir = "."
	case 1:
		wdir = flag.Arg(0)
	default:
		log.Fatalf("Expected 0 or 1 arguments, but found %d")
	}

	infos, err := ioutil.ReadDir(wdir)
	if err != nil {
		log.Fatalf("couldn't read directory %q: %v", wdir, err)
	}

	for _, info := range infos {
		if info.IsDir() {
			continue
		}
		widgets = append(widgets, &Widget{fileInfo: info})
	}
}

func updateUI(img draw.Image, keyEvent *KeyEvent, pointerEvent *PointerEvent) image.Rectangle {
	once.Do(getWidgets)

	var y = 8 // top padding

	// background color
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.ZP, draw.Src)

	for idx, widget := range widgets {
		if widget.fileInfo.Mode().Perm()&0111 != 0 { // executable
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

func button(state *ButtonState, text string, rect image.Rectangle, img draw.Image, pointerEvent *PointerEvent) bool {
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

func edit(state *EditorState, text *string, rect image.Rectangle, img draw.Image, keyEvent *KeyEvent, pointerEvent *PointerEvent) {
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
