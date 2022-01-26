package main

import (
	"fmt"
	"os"
	"path"
)

type Buffer struct {
	bf [2]byte
	f  *os.File
}

func (bf *Buffer) advance() {
	data := []byte{}
	data = make([]byte, 1)
	_, err := bf.f.Read(data)
	// Since we need to read the EOI marker before we get to the end of the file
	// We just os.Exit(1) if we get get to the end of file first
	if err != nil {
		fmt.Printf("Error! %s\n", err.Error())
		os.Exit(1)
	}
	bf.bf[1] = bf.bf[0]
	bf.bf[0] = data[0]
}

func decodeAPPN(header *Header) {
	fmt.Printf("** Decoding APPN Marker (0xFF%X) **\n", header.buffer.bf[0])
	buf := header.buffer
	buf.advance()
	buf.advance()
	// Length includes the 2 bytes that give you the length
	length := (int(buf.bf[1]) << 8) + int(buf.bf[0]) - 2
	for a := 0; a < length; a++ {
		buf.advance()
	}
}

func decodeQuantizationTables(header *Header) {
	buf := header.buffer
	fmt.Printf("** Decoding the DQT Marker (0xFF%X) **\n", buf.bf[0])
	buf.advance()
	buf.advance()
	length := (int(buf.bf[1]) << 8) + int(buf.bf[0]) - 2
	for {
		if length <= 0 {
			break
		}
		buf.advance()
		tableId := int(buf.bf[0] & 0x0F)
		length -= 1
		if tableId > 3 {
			fmt.Printf("Error! Ivalid TableId: %d\n", tableId)
			os.Exit(1)
		}
		// If the upper nibble is non-zero then the table is 16bit
		bit16 := (buf.bf[0] >> 4) != 0
		table := [64]byte{}
		if bit16 {
			for a := 0; a < 64; a++ {
				buf.advance()
				buf.advance()
				table[zigzag[a]] = (buf.bf[1] << 8) + buf.bf[0]
			}
			length -= 128
		} else {
			for a := 0; a < 64; a++ {
				buf.advance()
				table[zigzag[a]] = buf.bf[0]
			}
			length -= 64
		}
		// Check if a table with the same ID already exist
		for t := range header.qTables {
			if tableId == header.qTables[t].Id {
				fmt.Printf("Error! More than one Quantization Table with the same ID: %d\n", tableId)
				os.Exit(1)
			}
		}
		header.qTables = append(header.qTables, QuantizationTable{Id: tableId, table: table})
	}
}

func decodeStartOfFrame(h *Header) {
	fmt.Printf("** Decoding Start Of Frame (0xFF%X) **\n", h.buffer.bf[0])
	buf := h.buffer
	buf.advance()
	buf.advance()
	length := (int(buf.bf[1]) << 8) + int(buf.bf[0]) - 2
	buf.advance()
	length -= 1
	if buf.bf[0] != 8 {
		fmt.Printf("Error! Invalid Precision (%d). Expected 8\n", buf.bf[0])
		os.Exit(1)
	}
	buf.advance()
	buf.advance()
	length -= 2
	width := (int(buf.bf[1]) << 8) + int(buf.bf[0])
	buf.advance()
	buf.advance()
	length -= 2
	height := (int(buf.bf[1]) << 8) + int(buf.bf[0])
	buf.advance()
	length -= 1
	components := int(buf.bf[0])
	if components > 3 {
		fmt.Printf("Error! Number of components > 3. CMYK ColorMode not supported\n")
		os.Exit(1)
	}
	for a := 0; a < components; a++ {
		buf.advance()
		length -= 1
		// TODO: Add Error handling for when compId > 3 and of YIQ color mode (id=4,5)
		compId := int(buf.bf[0])
		buf.advance()
		length -= 1
		hSamplingFactor := int(buf.bf[0]) >> 4
		vSamplingFactor := int(buf.bf[0]) & 0x0F
		buf.advance()
		length -= 1
		qTableId := int(buf.bf[0])
		// Check if the Id is zero based
		if compId == 0 {
			h.zeroBased = true
		}
		// Check if the component alredy exist, Duplicate Ids
		for c := range h.cComponents {
			comp := &h.cComponents[c]
			if compId == comp.Id {
				fmt.Printf("Error! Duplicate coponentId (%d) found when scanning 'START OF FRAME'\n", compId)
				os.Exit(1)
			}
		}

		h.cComponents = append(h.cComponents, ColorComponent{
			Id:              compId,
			vSamplingFactor: vSamplingFactor,
			hSamplingFactor: hSamplingFactor,
			qTableId:        qTableId,
		})
	}
	h.width = width
	h.height = height
	if h.zeroBased {
		for a := range h.cComponents {
			h.cComponents[a].Id += 1
		}
	}
	if length != 0 {
		fmt.Printf("Error! Invalid Start Of Frame\n")
	}
}

func decodeRestartInterval(header *Header) {
	buf := header.buffer
	fmt.Printf("** Decoding Define Restart Interval (0xFF%X)**\n", buf.bf[0])
	buf.advance()
	buf.advance()
	length := (int(buf.bf[1]) << 8) + int(buf.bf[0]) - 2
	if length != 2 {
		fmt.Printf("Error! Invalid Restart Interval Length (%d)\n", length)
		os.Exit(1)
	}
	buf.advance()
	buf.advance()
	restartInterval := (int(buf.bf[1]) << 8) + int(buf.bf[0])
	header.restartInterval = restartInterval
}

func decodeDefineHuffmanTable(header *Header) {
	buf := header.buffer
	fmt.Printf("** Decoding Define Huffman Table (0xFF%X) **\n", buf.bf[0])
	buf.advance()
	buf.advance()
	length := (int(buf.bf[1]) << 8) + int(buf.bf[0]) - 2
	for {
		if length <= 0 {
			break
		}
		buf.advance()
		length -= 1
		dc := (buf.bf[0] >> 4) == 0
		tableId := int(buf.bf[0] & 0x0F)
		// Create the new table
		table := HuffmanTable{
			Id: tableId,
			dc: dc,
		}
		// Read the codes of len
		count := 0
		for a := 0; a < 16; a++ {
			buf.advance()
			length -= 1
			table.codesOfLen[a] = int(buf.bf[0])
			count += int(buf.bf[0])
		}
		for a := 0; a < count; a++ {
			buf.advance()
			length -= 1
			table.symbols = append(table.symbols, buf.bf[0])
		}
		header.huffmanTables = append(header.huffmanTables, table)
	}
	if length != 0 {
		fmt.Printf("Error! Invalid DefineHuffanTable Marker\n")
	}
}

func endScan(header *Header) {
	buf := header.buffer
	fmt.Printf("*** EOI (0xFF%X) ***\n", buf.bf[0])
	header.print()
}

func decodeStartOfScan(header *Header) {
	buf := header.buffer
	fmt.Printf("** Decoding Start of Scan (0xFF%X) **\n", buf.bf[0])
	buf.advance()
	buf.advance()
	length := (int(buf.bf[1]) << 8) + int(buf.bf[0]) - 2
	buf.advance()
	length -= 1
	components := int(buf.bf[0])
	for a := 0; a < components; a++ {
		buf.advance()
		length -= 1
		compId := int(buf.bf[0])
		if header.zeroBased {
			compId += 1
		}
		buf.advance()
		length -= 1
		dcHuffmanTableId := buf.bf[0] >> 4
		acHuffmanTableId := buf.bf[0] & 0x0F
		// Assign the AC and DC Huffman Table Ids to the components
		for c := range header.cComponents {
			comp := &header.cComponents[c]
			if compId == comp.Id {
				comp.acHuffmanTableId = int(acHuffmanTableId)
				comp.dcHuffmanTableId = int(dcHuffmanTableId)
			}
		}
	}
	buf.advance()
	length -= 1
	header.startOfSelection = buf.bf[0]
	buf.advance()
	length -= 1
	header.endOfSelection = buf.bf[0]
	buf.advance()
	length -= 1
	header.successiveApproximationHigh = buf.bf[0] >> 4
	header.successiveApproximationLow = buf.bf[0] & 0x0F
	/** Begin the SCAN **/
	buf.advance()
	for {
		// The only markers that are allowed in the scan are RSTn Markers and the EOI Marker
		if buf.bf[0] == 0xFF {
			buf.advance()
			if buf.bf[0] == 0xFF {
				buf.advance()
				continue
			} else if buf.bf[0] >= RST0 && buf.bf[0] <= RST7 {
				buf.advance()
				continue
			} else if buf.bf[0] == EOI {
				endScan(header)
				break
			} else if buf.bf[0] == 0x00 {
				header.bitstream = append(header.bitstream, 0xFF)
				buf.advance()
				continue
			}
			fmt.Printf("Ivalid Byte (0xFF%X) found in the bitsteam\n", buf.bf[0])
			os.Exit(1)
		}
		header.bitstream = append(header.bitstream, buf.bf[0])
		buf.advance()
	}
}

func skipMarker(header *Header) {
	buf := header.buffer
	buf.advance()
	buf.advance()
	length := (int(buf.bf[1]) << 8) + int(buf.bf[0]) - 2
	for a := 0; a < length; a++ {
		buf.advance()
	}
	fmt.Printf("Skipped Marker (0xFF%X) Len (#%d) Bytes\n", buf.bf[0], length)
}

func decodeJPEG(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Error! %s\n", err.Error())
		os.Exit(1)
	}
	stat, _ := file.Stat()
	wd, _ := os.Getwd()
	_filename := path.Join(wd, file.Name())
	_filesize := stat.Size()
	buffer := Buffer{f: file}
	// Create the header
	header := &Header{
		filename: _filename,
		filesize: uint(_filesize),
		buffer:   &buffer,
	}
	buffer.advance()
	buffer.advance()
	if buffer.bf[1] != 0xFF && buffer.bf[0] != SOI {
		fmt.Printf("Error! The file is not a valid JPEG\n")
		os.Exit(1)
	}
	// For loop for parsig all the markers
	buffer.advance()
	buffer.advance()
	for {
		if buffer.bf[1] != 0xFF {
			fmt.Printf("Error! Expected a Marker but found byte (%x)\n", buffer.bf[1])
			os.Exit(1)
		}
		// The standard allows for any number of 0xFF bytes to precede the marker
		if buffer.bf[0] == 0xFF {
			buffer.advance()
			continue
		} else if buffer.bf[0] >= APP0 && buffer.bf[0] <= APP15 {
			decodeAPPN(header)
		} else if buffer.bf[0] == DQT {
			decodeQuantizationTables(header)
		} else if buffer.bf[0] == SOF0 {
			decodeStartOfFrame(header)
		} else if buffer.bf[0] == DRI {
			decodeRestartInterval(header)
		} else if buffer.bf[0] == DHT {
			decodeDefineHuffmanTable(header)
		} else if buffer.bf[0] == SOS {
			decodeStartOfScan(header)
			break
		} else if (buffer.bf[0] >= JPG0 && buffer.bf[0] <= JPG13) ||
			(buffer.bf[0] == DNL) ||
			(buffer.bf[0] == DHP) ||
			(buffer.bf[0] == EXP) ||
			(buffer.bf[0] == COM) {
			skipMarker(header)
		} else if buffer.bf[0] == TEM {
			// TEM has no size nor payload
		} else if buffer.bf[0] == EOI {
			fmt.Printf("Error! Found EOI Marker (0xFF%X) before the Start Of Scan Marker\n", buffer.bf[0])
			os.Exit(1)
		} else if buffer.bf[0] == SOI {
			fmt.Printf("Error! Embeded JPG not supported\n")
			os.Exit(1)
		} else if buffer.bf[0] == DAC {
			fmt.Printf("Error! Arithmetic Coding not supported\n")
			os.Exit(1)
		} else if buffer.bf[0] >= SOF0 && buffer.bf[0] <= SOF15 {
			fmt.Printf("Error! SOF Marker (0xFF%X) not supported\n", buffer.bf[0])
			os.Exit(1)
		} else {
			fmt.Printf("Invalid Marker (0xFF%X)\n", buffer.bf[0])
			os.Exit(1)
		}
		buffer.advance()
		buffer.advance()
	}
	file.Close()
}

func pad(bt byte) string {
	val := fmt.Sprintf("%d", int(bt))
	rem := 3 - len(val)
	for a := 0; a < rem; a++ {
		val = " " + val
	}
	return val
}

var zigzag = [64]byte{
	0, 1, 8, 16, 9, 2, 3, 10,
	17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34,
	27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36,
	29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

func (header *Header) print() {
	fmt.Printf("\n")
	fmt.Printf("Filename : [%s]\n", header.filename)
	fmt.Printf("Filesize : [%d] Bytes\n", header.filesize)
	fmt.Printf("\n")
	fmt.Printf("Start Of Selection            : %d\n", header.startOfSelection)
	fmt.Printf("End of Selection              : %d\n", header.endOfSelection)
	fmt.Printf("Successive Approximation High : %d\n", header.successiveApproximationHigh)
	fmt.Printf("Successive Approximation Low  : %d\n", header.successiveApproximationLow)
	fmt.Printf("\n")
	fmt.Printf("*** Quantization Tables ***\n")
	for t := range header.qTables {
		table := header.qTables[t]
		fmt.Printf("Id (%d)\n", table.Id)
		for entry := range table.table {
			if entry%8 == 0 {
				fmt.Printf("\n")
			}
			fmt.Printf("%s", pad(table.table[entry]))
		}
		fmt.Printf("\n\n")
	}
	fmt.Printf("*** Start Of Frame ***\n")
	fmt.Printf("Size       : %d x %d\n", header.width, header.height)
	fmt.Printf("Components : %d\n", len(header.cComponents))
	for a := range header.cComponents {
		comp := header.cComponents[a]
		fmt.Printf("** ComponentId (%d) **\n", comp.Id)
		fmt.Printf("Horizontal Sampling Factor : %d\n", comp.hSamplingFactor)
		fmt.Printf("Vertical Sampling Factor   : %d\n", comp.vSamplingFactor)
		fmt.Printf("Quantization Table Id      : %d\n", comp.qTableId)
		fmt.Printf("AC Huffman Table Id        : %d\n", comp.acHuffmanTableId)
		fmt.Printf("DC Huffman Table Id        : %d\n", comp.dcHuffmanTableId)
	}
	fmt.Printf("\n*** Huffman Tables ***\n")
	for t := range header.huffmanTables {
		table := header.huffmanTables[t]
		fmt.Printf("* Id : (%d) ", table.Id)
		if table.dc {
			fmt.Printf("DC *\n")
		} else {
			fmt.Printf("AC *\n")
		}
		lastIndex := 0
		for a := 0; a < 16; a++ {
			fmt.Printf("%s  --> ", pad(byte(a+1)))
			for s := range table.symbols[lastIndex : lastIndex+table.codesOfLen[a]] {
				fmt.Printf("%x ", table.symbols[s+lastIndex])
			}
			lastIndex += table.codesOfLen[a]
			fmt.Printf("\n")
		}
		fmt.Printf("\n")
	}
	fmt.Printf("Huffman data length (%d) Bytes\n", len(header.bitstream))
}

// Markers
const (
	// Start of Frame markers, non-differential, Huffman coding
	SOF0 = 0xC0 // Baseline DCT
	SOF1 = 0xC1 // Extended sequential DCT
	SOF2 = 0xC2 // Progressive DCT
	SOF3 = 0xC3 // Lossless (sequential)

	// Start of Frame markers, differential, Huffman coding
	SOF5 = 0xC5 // Differential sequential DCT
	SOF6 = 0xC6 // Differential progressive DCT
	SOF7 = 0xC7 // Differential lossless (sequential)

	// Start of Frame markers, non-differential, arithmetic coding
	SOF9  = 0xC9 // Extended sequential DCT
	SOF10 = 0xCA // Progressive DCT
	SOF11 = 0xCB // Lossless (sequential)

	// Start of Frame markers, differential, arithmetic coding
	SOF13 = 0xCD // Differential sequential DCT
	SOF14 = 0xCE // Differential progressive DCT
	SOF15 = 0xCF // Differential lossless (sequential)

	// Define Huffman Table(s)
	DHT = 0xC4

	// JPEG extensions
	JPG = 0xC8

	// Define Arithmetic Coding Conditioning(s)
	DAC = 0xCC

	// Restart interval Markers
	RST0 = 0xD0
	RST1 = 0xD1
	RST2 = 0xD2
	RST3 = 0xD3
	RST4 = 0xD4
	RST5 = 0xD5
	RST6 = 0xD6
	RST7 = 0xD7

	// Other Markers
	SOI = 0xD8 // Start of Image
	EOI = 0xD9 // End of Image
	SOS = 0xDA // Start of Scan
	DQT = 0xDB // Define Quantization Table(s)
	DNL = 0xDC // Define Number of Lines
	DRI = 0xDD // Define Restart Interval
	DHP = 0xDE // Define Hierarchical Progression
	EXP = 0xDF // Expand Reference Component(s)

	// APPN Markers
	APP0  = 0xE0
	APP1  = 0xE1
	APP2  = 0xE2
	APP3  = 0xE3
	APP4  = 0xE4
	APP5  = 0xE5
	APP6  = 0xE6
	APP7  = 0xE7
	APP8  = 0xE8
	APP9  = 0xE9
	APP10 = 0xEA
	APP11 = 0xEB
	APP12 = 0xEC
	APP13 = 0xED
	APP14 = 0xEE
	APP15 = 0xEF

	// Misc Markers
	JPG0  = 0xF0
	JPG1  = 0xF1
	JPG2  = 0xF2
	JPG3  = 0xF3
	JPG4  = 0xF4
	JPG5  = 0xF5
	JPG6  = 0xF6
	JPG7  = 0xF7
	JPG8  = 0xF8
	JPG9  = 0xF9
	JPG10 = 0xFA
	JPG11 = 0xFB
	JPG12 = 0xFC
	JPG13 = 0xFD
	COM   = 0xFE
	TEM   = 0x01
)

type QuantizationTable struct {
	table [64]byte
	Id    int
}

type HuffmanTable struct {
	Id         int
	symbols    []byte
	codesOfLen [16]int
	dc         bool
}

type Header struct {
	filename                    string
	filesize                    uint
	buffer                      *Buffer
	qTables                     []QuantizationTable
	cComponents                 []ColorComponent
	width                       int
	height                      int
	restartInterval             int
	huffmanTables               []HuffmanTable
	startOfSelection            byte
	endOfSelection              byte
	successiveApproximationHigh byte
	successiveApproximationLow  byte
	bitstream                   []byte
	zeroBased                   bool
}

type ColorComponent struct {
	Id               int
	hSamplingFactor  int
	vSamplingFactor  int
	qTableId         int
	acHuffmanTableId int
	dcHuffmanTableId int
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Error! No file given\n")
		os.Exit(1)
	}
	fmt.Printf("***** JPEG Decoder by Maxwell Mbugua *****\n\n")
	filenames := os.Args[1:]
	for a := range filenames {
		decodeJPEG(filenames[a])
	}
}
