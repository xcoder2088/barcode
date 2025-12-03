package main

import (
	"embed"
	"fmt"
	"html/template"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/fogleman/gg"
)

// Embed the template files into the binary
//
//go:embed templates/*
var content embed.FS

const baseDPI = 96

// Load font from static fonts directory dynamically based on working directory
func loadFontFromStatic(dc *gg.Context, fontName string, size float64) error {
	// Get the current working directory
	currentDir, err := os.Getwd() // Get the working directory
	if err != nil {
		return fmt.Errorf("unable to get current directory: %v", err)
	}

	// Construct the absolute path to the fonts directory
	fontPath := filepath.Join(currentDir, "static", "fonts", fontName)
	fmt.Println("Trying to load font from:", fontPath) // Log the file path for debugging

	// Check if the font file exists
	if _, err := os.Stat(fontPath); os.IsNotExist(err) {
		return fmt.Errorf("font not found at: %s", fontPath)
	}

	// Load the font face
	err = dc.LoadFontFace(fontPath, size)
	if err != nil {
		return fmt.Errorf("failed to load font face: %v", err)
	}

	return nil
}

// BarcodeData holds the properties for each barcode
type BarcodeData struct {
	Data         string
	Width        int
	Height       int
	PaddingColor string
	FontChoice   string
	TextColor    string
	TextSize     int
	Bold         bool
}

// Parse HEX color to color.RGBA
func parseHexColor(s string) (color.RGBA, error) {
	c, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.RGBA{}, err
	}
	return color.RGBA{uint8(c >> 16), uint8(c >> 8 & 0xFF), uint8(c & 0xFF), 0xFF}, nil
}

// Handle barcode generation
func generateBarcode(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	var barcodes []BarcodeData

	// Loop to capture up to 4 barcode inputs
	for i := 1; i <= 4; i++ {
		data := r.FormValue(fmt.Sprintf("data%d", i))
		if data == "" {
			continue
		}

		width, _ := strconv.Atoi(r.FormValue(fmt.Sprintf("width%d", i)))
		height, _ := strconv.Atoi(r.FormValue(fmt.Sprintf("height%d", i)))
		paddingColor := r.FormValue(fmt.Sprintf("padding_color%d", i))
		fontChoice := r.FormValue(fmt.Sprintf("font_choice%d", i))
		textColor := r.FormValue(fmt.Sprintf("text_color%d", i))
		textSize, _ := strconv.Atoi(r.FormValue(fmt.Sprintf("text_size%d", i)))
		bold := r.FormValue(fmt.Sprintf("bold%d", i)) == "on"

		barcodes = append(barcodes, BarcodeData{
			Data:         data,
			Width:        width,
			Height:       height,
			PaddingColor: paddingColor,
			FontChoice:   fontChoice,
			TextColor:    textColor,
			TextSize:     textSize,
			Bold:         bold,
		})
	}

	// Validate if any barcodes were added
	if len(barcodes) == 0 {
		http.Error(w, "No barcode data provided", http.StatusBadRequest)
		return
	}

	// Calculate total canvas size
	totalWidth := 0
	totalHeight := 0
	for _, barcode := range barcodes {
		totalWidth += barcode.Width + (barcode.TextSize * 2)
		if barcode.Height+(barcode.TextSize*3) > totalHeight {
			totalHeight = barcode.Height + (barcode.TextSize * 3)
		}
	}

	// Create drawing context
	dc := gg.NewContext(totalWidth, totalHeight)
	xOffset := 0

	for _, b := range barcodes {
		// Parse padding and text colors
		paddingColor, err := parseHexColor(b.PaddingColor)
		if err != nil {
			http.Error(w, "Invalid padding color", http.StatusBadRequest)
			return
		}
		textColor, err := parseHexColor(b.TextColor)
		if err != nil {
			http.Error(w, "Invalid text color", http.StatusBadRequest)
			return
		}

		// Generate barcode
		bar, err := code128.Encode(b.Data)
		if err != nil {
			http.Error(w, "Failed to generate barcode", http.StatusInternalServerError)
			return
		}

		widthAtDPI := b.Width * baseDPI / 96
		heightAtDPI := b.Height * baseDPI / 96
		scaledBar, err := barcode.Scale(bar, widthAtDPI, heightAtDPI)
		if err != nil {
			http.Error(w, "Failed to scale barcode", http.StatusInternalServerError)
			return
		}

		// Draw background and barcode image
		dc.SetColor(paddingColor)
		dc.DrawRectangle(float64(xOffset), 0, float64(b.Width+(b.TextSize*2)), float64(totalHeight))
		dc.Fill()
		dc.DrawImage(scaledBar, xOffset+b.TextSize, b.TextSize)

		// Draw the barcode text
		dc.SetColor(textColor)

		// Load appropriate font from static fonts
		fontFile := "ARIAL.TTF" // Regular Arial
		if b.Bold {
			fontFile = "ARIBLK.TTF" // Arial Black
		}

		if err := loadFontFromStatic(dc, fontFile, float64(b.TextSize)); err != nil {
			http.Error(w, fmt.Sprintf("Failed to load font from static directory: %v", err), http.StatusInternalServerError)
			return
		}

		// Draw the barcode text
		textX := float64(xOffset + b.TextSize + (b.Width / 2))
		textY := float64(b.TextSize + b.Height + b.TextSize)
		dc.DrawStringAnchored(b.Data, textX, textY, 0.5, 0.5)

		xOffset += b.Width + (b.TextSize * 2)
	}

	// Save barcode image to temp folder with a unique name using timestamp
	tempDir := os.TempDir()
	fileName := fmt.Sprintf("generated_barcode_%d.png", time.Now().UnixNano())
	filePath := filepath.Join(tempDir, fileName)
	outFile, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Failed to save barcode", http.StatusInternalServerError)
		return
	}
	defer outFile.Close()
	if err := png.Encode(outFile, dc.Image()); err != nil {
		http.Error(w, "Failed to encode image", http.StatusInternalServerError)
		return
	}

	// Serve the generated barcode in the generated_barcode.html page
	tmpl, err := template.ParseFS(content, "templates/generated_barcode.html")
	if err != nil {
		http.Error(w, "Error parsing template", http.StatusInternalServerError)
		return
	}

	// Pass the local server path to the template for rendering
	barcodeURL := "/barcode_image?file=" + fileName
	tmpl.Execute(w, struct{ BarcodePath string }{BarcodePath: barcodeURL})
}

// Serve the form
func serveForm(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFS(content, "templates/index.html")
	if err != nil {
		http.Error(w, "Error parsing template", http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

// Serve the generated barcode image from the temp folder
func serveBarcodeImage(w http.ResponseWriter, r *http.Request) {
	fileName := r.URL.Query().Get("file")
	if fileName == "" {
		http.Error(w, "File not specified", http.StatusBadRequest)
		return
	}

	// Get the full path of the barcode in the temp folder
	filePath := filepath.Join(os.TempDir(), fileName)

	// Serve the file
	http.ServeFile(w, r, filePath)
}

func main() {
	// Serve static files
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))
	http.HandleFunc("/", serveForm)
	http.HandleFunc("/barcode", generateBarcode)
	http.HandleFunc("/barcode_image", serveBarcodeImage)

	fmt.Println("FCS Barcode Generator is Alive! Navigate to http://localhost:8080 to Generate your barcode..;) b.rad.year.2070@gmail.com")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Println("Server failed to start:", err)
	}
}
