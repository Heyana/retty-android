// Mkicon exports the approved Retty artwork for the native app bundles.
//
//	GOWORK=off go run ./tools/mkicon
//
// macOS gets a 1024px canvas with an inset squircle and transparent margins;
// iOS gets a full-bleed opaque 1024px image for the system's corner mask.
package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/vector"
)

const (
	size       = 1024
	sourcePath = "assets/branding/retty-source.png"
)

func main() {
	if err := exportIcons(); err != nil {
		log.Fatal(err)
	}
}

func exportIcons() error {
	f, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		return err
	}
	if src.Bounds().Dx() != src.Bounds().Dy() {
		return fmt.Errorf("approved artwork must be square: %v", src.Bounds())
	}

	// Flatten defensively: App Store/iOS icons must have no transparent pixels.
	ios := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(ios, ios.Bounds(), image.NewUniform(color.RGBA{0x10, 0x2b, 0x2d, 255}), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(ios, ios.Bounds(), src, src.Bounds(), draw.Over, nil)
	if err := writePNG("resources/ios/app-icon.png", ios); err != nil {
		return err
	}

	// Keep the same artwork and proportions inside the native macOS icon grid.
	const inset, width = 100, 824
	tile := image.Rect(inset, inset, inset+width, inset+width)
	art := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(art, tile, ios, ios.Bounds(), draw.Src, nil)
	mask := image.NewAlpha(art.Bounds())
	squircle(inset, inset, width).Draw(mask, mask.Bounds(), image.NewUniform(color.White), image.Point{})
	mac := image.NewNRGBA(art.Bounds())
	draw.DrawMask(mac, mac.Bounds(), art, image.Point{}, mask, image.Point{}, draw.Src)
	if err := writePNG("resources/icon.png", mac); err != nil {
		return err
	}
	// Tahoe uses an Icon Composer stack. Older macOS must never receive the
	// unmasked full-bleed artwork as a classic asset-catalog app icon.
	if runtime.GOOS == "darwin" {
		return exportMacCatalog(ios)
	}
	return nil
}

func exportMacCatalog(src image.Image) error {
	work, err := os.MkdirTemp("", "retty-icon-catalog-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	icon := filepath.Join(work, "RettyAppIcon.icon")
	if err := os.MkdirAll(filepath.Join(icon, "Assets"), 0o755); err != nil {
		return err
	}
	if err := writePNG(filepath.Join(icon, "Assets", "Retty.png"), src); err != nil {
		return err
	}
	document := map[string]any{
		"fill": map[string]any{"solid": "srgb:0.06275,0.16863,0.17647,1.00000"},
		"groups": []any{map[string]any{
			"blur-material": nil,
			"layers": []any{map[string]any{
				"image-name": "Retty.png", "name": "Retty", "glass": false,
			}},
			"shadow":       map[string]any{"kind": "neutral", "opacity": 0},
			"specular":     false,
			"translucency": map[string]any{"enabled": false, "value": 0},
		}},
		"supported-platforms": map[string]any{"squares": []string{"macOS"}},
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(icon, "icon.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	out := filepath.Join(work, "compiled")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("xcrun", "actool", icon, "--compile", out, "--platform", "macosx", "--target-device", "mac", "--minimum-deployment-target", "13.0", "--app-icon", "RettyAppIcon", "--enable-icon-stack-fallback-generation=disabled", "--output-partial-info-plist", filepath.Join(out, "partial.plist"))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile macOS icon catalog: %w\n%s", err, output)
	}
	// Some Xcode versions also emit classic fallback renditions. Validate the
	// compiled catalog, since those take precedence over ICNS on older macOS.
	metadata, err := exec.Command("xcrun", "assetutil", "--info", filepath.Join(out, "Assets.car")).Output()
	if err != nil {
		return fmt.Errorf("inspect macOS icon catalog: %w", err)
	}
	var renditions []struct {
		AssetType string
		Name      string
		Opaque    *bool
	}
	if err := json.Unmarshal(metadata, &renditions); err != nil {
		return err
	}
	hasStack := false
	for _, r := range renditions {
		if r.Name != "RettyAppIcon" {
			continue
		}
		hasStack = hasStack || r.AssetType == "IconImageStack"
		if r.AssetType == "Icon Image" && (r.Opaque == nil || *r.Opaque) {
			return fmt.Errorf("macOS icon catalog contains an unmasked classic icon")
		}
	}
	if !hasStack {
		return fmt.Errorf("macOS icon catalog is missing the Tahoe icon stack")
	}
	data, err = os.ReadFile(filepath.Join(out, "Assets.car"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll("resources/darwin", 0o755); err != nil {
		return err
	}
	return os.WriteFile("resources/darwin/Assets.car", data, 0o644)
}

func writePNG(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// A superellipse preserves continuous corners instead of a circular radius.
func squircle(x, y, width float32) *vector.Rasterizer {
	r := vector.NewRasterizer(size, size)
	cx, cy, a := float64(x+width/2), float64(y+width/2), float64(width/2)
	for i := 0; i <= 720; i++ {
		t := float64(i) / 720 * 2 * math.Pi
		c, s := math.Cos(t), math.Sin(t)
		px := cx + a*math.Copysign(math.Pow(math.Abs(c), 0.4), c)
		py := cy + a*math.Copysign(math.Pow(math.Abs(s), 0.4), s)
		if i == 0 {
			r.MoveTo(float32(px), float32(py))
		} else {
			r.LineTo(float32(px), float32(py))
		}
	}
	r.ClosePath()
	return r
}
