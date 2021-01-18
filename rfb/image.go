package rfb

import (
	"image"
	"image/color"
	"fmt"
	"encoding/binary"
)

type PixelFormatImage struct {
	Pix         []uint8
	Rect        image.Rectangle
	PixelFormat PixelFormat
}

func NewPixelFormatImage(pixelFormat PixelFormat, bounds image.Rectangle) *PixelFormatImage {
	bytesPerPixel := int(pixelFormat.BitsPerPixel / 8)
	return &PixelFormatImage{make([]uint8, bytesPerPixel*bounds.Dx()*bounds.Dy()), bounds, pixelFormat}
}

func (img *PixelFormatImage) ColorModel() color.Model {
	panic("not implemented")
}

func (img *PixelFormatImage) Bounds() image.Rectangle {
	return img.Rect
}

func (img *PixelFormatImage) At(x, y int) color.Color {
	idx := img.idx(x, y)
	bo := img.bo()
	var pixel uint32
	switch img.PixelFormat.BitsPerPixel {
	case 8:
		pixel = uint32(img.Pix[idx])
	case 16:
		pixel = uint32(bo.Uint16(img.Pix[idx:]))
	case 32:
		pixel = bo.Uint32(img.Pix[idx:])
	default:
		panic(fmt.Sprintf("BitsPerPixel must be 8, 16, or 32, but it's %d", img.PixelFormat.BitsPerPixel))
	}
	r := (pixel >> img.PixelFormat.RedShift) & uint32(img.PixelFormat.RedMax)
	g := (pixel >> img.PixelFormat.GreenShift) & uint32(img.PixelFormat.GreenMax)
	b := (pixel >> img.PixelFormat.BlueShift) & uint32(img.PixelFormat.BlueMax)
	if img.PixelFormat.RedMax != 255 || img.PixelFormat.GreenMax != 255 || img.PixelFormat.BlueMax != 255 {
		panic(fmt.Sprintf("max red, green, and blue must be 255, but are %d, %d, and %d", img.PixelFormat.RedMax, img.PixelFormat.GreenMax, img.PixelFormat.BlueMax))
	}
	return color.NRGBA{uint8(r), uint8(g), uint8(b), 0xff}
}

func (img *PixelFormatImage) Set(x, y int, c color.Color) {
	nrgba := color.NRGBAModel.Convert(c).(color.NRGBA)

	if img.PixelFormat.RedMax != 255 || img.PixelFormat.GreenMax != 255 || img.PixelFormat.BlueMax != 255 {
		panic(fmt.Sprintf("max red, green, and blue must be 255, but are %d, %d, and %d", img.PixelFormat.RedMax, img.PixelFormat.GreenMax, img.PixelFormat.BlueMax))
	}
	var pixel uint32
	pixel |= (uint32(nrgba.R) & uint32(img.PixelFormat.RedMax)) << img.PixelFormat.RedShift
	pixel |= (uint32(nrgba.G) & uint32(img.PixelFormat.GreenMax)) << img.PixelFormat.GreenShift
	pixel |= (uint32(nrgba.B) & uint32(img.PixelFormat.BlueMax)) << img.PixelFormat.BlueShift

	idx := img.idx(x, y)
	bo := img.bo()
	switch img.PixelFormat.BitsPerPixel {
	case 8:
		img.Pix[idx] = uint8(pixel)
	case 16:
		bo.PutUint16(img.Pix[idx:], uint16(pixel))
	case 32:
		bo.PutUint32(img.Pix[idx:], pixel)
	default:
		panic(fmt.Sprintf("BitsPerPixel must be 8, 16, or 32, but it's %d", img.PixelFormat.BitsPerPixel))
	}
}

func (img *PixelFormatImage) bo() binary.ByteOrder {
	if img.PixelFormat.BigEndian {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

func (img *PixelFormatImage) idx(x, y int) int {
	bytesPerPixel := int(img.PixelFormat.BitsPerPixel / 8)
	return (bytesPerPixel*img.Rect.Dx())*(y-img.Rect.Min.Y) + bytesPerPixel*(x-img.Rect.Min.X)
}
