// Copy the approved PNG unchanged; Wails derives platform icons from appicon.png.
package main

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
)

func main() {
	data, err := os.ReadFile("assets/branding/multipan-icon.png")
	if err != nil {
		panic(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	if img.Bounds().Dx() != img.Bounds().Dy() {
		panic("application icon must be square")
	}
	for _, path := range []string{"build/appicon.png", "frontend/public/icon.png"} {
		if err := os.WriteFile(path, data, 0644); err != nil {
			panic(err)
		}
	}
	fmt.Printf("Application PNG: %dx%d (original alpha preserved)\n", img.Bounds().Dx(), img.Bounds().Dy())
}
