package main

import (
	"dec/zmap"
	"fmt"
	"math"
	"os"
	"path"
	"strings"
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
	// Set the frameType of the image
	h.frameType = h.buffer.bf[0]
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
	height := (int(buf.bf[1]) << 8) + int(buf.bf[0])
	buf.advance()
	buf.advance()
	length -= 2
	width := (int(buf.bf[1]) << 8) + int(buf.bf[0])
	buf.advance()
	length -= 1
	components := int(buf.bf[0])
	if components > 3 {
		fmt.Printf("Error! Number of components > 3. CMYK ColorMode not supported\n")
		os.Exit(1)
	}
	// Set the width and the height
	h.width = width
	h.height = height

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
	if h.zeroBased {
		for a := range h.cComponents {
			h.cComponents[a].Id += 1
		}
	}

	comp := h.cComponents[0]
	// Check the scling factor of the Y channel and set the mcuWidth and Height
	if comp.hSamplingFactor == 1 {
		if comp.vSamplingFactor == 1 {
			h.mcuDimensions = _8x8
			h.mcuWidth = (h.width + 7) / 8
			h.mcuHeight = (h.height + 7) / 8
		} else if comp.vSamplingFactor == 2 {
			h.mcuDimensions = _8x16
			h.mcuWidth = (h.width + 7) / 8
			h.mcuHeight = (h.height + 15) / 16
		} else {
			fmt.Printf("Error! Invalid Sampling Factor\n")
		}
	} else if comp.hSamplingFactor == 2 {
		if comp.vSamplingFactor == 1 {
			h.mcuDimensions = _16x8
			h.mcuWidth = (h.width + 15) / 16
			h.mcuHeight = (h.height + 7) / 8
		} else if comp.vSamplingFactor == 2 {
			h.mcuDimensions = _16x16
			h.mcuHeight = (h.height + 15) / 16
			h.mcuWidth = (h.width + 15) / 16
		} else {
			fmt.Printf("Error! invalid Sampling Factor\n")
		}
	} else {
		fmt.Printf("Error! invalid Sampling Factor\n")
	}
	h.mcuCount = h.mcuWidth * h.mcuHeight
	// Check if len == 0
	if length != 0 {
		fmt.Printf("Error! Invalid Start Of Frame\n")
	}

}

func read64Coeffecients(header *Header, br *BitReader, acHuffmanTable *HuffmanTable, dcHuffmanTable *HuffmanTable, prevDC *int, skips *int) *[64]int {
	res := [64]int{}
	if header.frameType == SOF2 {
		// Progressive JPGs
		if header.startOfSelection == 0 && header.successiveApproximationHigh == 0 {
			/** DC First Visit **/
			sym := scanSymbol(br, dcHuffmanTable)
			dcLength := int(sym)
			dcCoeffecient := br.readBits(dcLength)
			if dcLength != 0 && dcCoeffecient < (1<<(dcLength-1)) {
				dcCoeffecient -= (1<<dcLength - 1)
			}
			dcCoeffecient += *prevDC
			*prevDC = dcCoeffecient
			res[0] = dcCoeffecient << header.successiveApproximationHigh
		} else if header.startOfSelection != 0 && header.successiveApproximationHigh == 0 {
			if *skips > 0 {
				*skips -= 1
				return &res
			}
			// start at the start of selection of the band
			start := int(header.startOfSelection)
			end := int(header.endOfSelection)
			index := start
			for {
				// end at the header end of selection
				if index > end {
					break
				}
				sym := scanSymbol(br, acHuffmanTable)
				if sym == 0xff {
					fmt.Printf("Error! Invalid symbol 0xff found\n")
					os.Exit(1)
				}
				switch sym {
				case 0xF0:
					// 0xF0 means the next 16 coeffecients are 0
					max := index + 16
					for a := index; a < max; a++ {
						res[a] = 0
						index++
					}
				default:
					numZeros := int(sym >> 4)
					acLength := int(sym & 0x0F)
					if acLength != 0 {
						max := index + numZeros
						for a := index; a < max; a++ {
							res[a] = 0
							index++
						}
						acCoeffecient := br.readBits(acLength)
						if acCoeffecient < (1 << (acLength - 1)) {
							acCoeffecient -= (1<<acLength - 1)
						}
						res[index] = acCoeffecient
						index++
					} else {
						_skips := (1 << numZeros) - 1
						_extra := br.readBits(numZeros)
						if _extra == 0xff {
							fmt.Printf("Error! Invalid EOB\n")
							os.Exit(1)
						}
						_skips += _extra
						*skips = _skips
						// Once you have reached the end-of-band marker you should return &res
						// this is because you are done with the current block
						return &res
					}

				}
			}
		}
		return &res
	}
	return &res
}

// Helper function to get the correct *HuffmanTable
func getTable(header *Header, dc bool, Id int) *HuffmanTable {
	for t := range header.huffmanTables {
		tab := header.huffmanTables[t]
		if Id == tab.Id && dc == tab.dc {
			return &tab
		}
	}
	return nil
}

// Helper function to get the correct quantization table
func getQuantizationTable(header *Header, index int) *QuantizationTable {
	tId := header.cComponents[index].qTableId
	for a := range header.qTables {
		t := header.qTables[a]
		if t.Id == tId {
			return &t
		}
	}
	return nil
}

// Helper function to perform inverse DCT on 8x8 block
func inverseDCTPixel(x int, y int, channel *[64]int) int {
	sum := float64(0)
	for u := 0; u < 8; u++ {
		for v := 0; v < 8; v++ {
			coeff := float64((*channel)[u+v*8])
			cos1 := math.Cos((float64((2*x + 1)) * float64(u) * float64(math.Pi)) / 16)
			cos2 := math.Cos((float64((2*y + 1)) * float64(v) * float64(math.Pi)) / 16)
			if u == 0 {
				if v == 0 {
					sum += (float64(1) / float64(2)) * coeff * cos1 * cos2
				} else {
					sum += (float64(1) / math.Sqrt(2)) * coeff * cos1 * cos2
				}
			} else {
				if v == 0 {
					sum += (float64(1) / math.Sqrt(2)) * coeff * cos1 * cos2
				} else {
					sum += coeff * cos1 * cos2
				}
			}
		}
	}
	return int(sum * 0.25)
}

func inverseDCT(header *Header) {
	for a := range header.MCUArray {
		mcu := &header.MCUArray[a]
		arr := mcu.getArraySections(header)
		switch header.mcuDimensions {
		case _8x8:
			arr1 := [64]int{}
			arr2 := [64]int{}
			arr3 := [64]int{}

			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					arr1[x+8*y] = inverseDCTPixel(x, y, &arr[0])
					arr2[x+8*y] = inverseDCTPixel(x, y, &arr[1])
					arr3[x+8*y] = inverseDCTPixel(x, y, &arr[2])
				}
			}

			// reset the values
			index := 0
			for k := 0; k < 8; k++ {
				base := 8 * k
				for j := base; j < base+8; j++ {
					(*mcu).ch1[j] = arr1[index]
					(*mcu).ch2[j] = arr2[index]
					(*mcu).ch3[j] = arr3[index]
					index++
				}
			}
		case _16x8:
			arr1 := [64]int{}
			arr2 := [64]int{}
			arr3 := [64]int{}
			arr4 := [64]int{}
			arr5 := [64]int{}
			arr6 := [64]int{}

			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					arr1[x+y*8] = inverseDCTPixel(x, y, &arr[0])
					arr2[x+y*8] = inverseDCTPixel(x, y, &arr[1])
					arr3[x+y*8] = inverseDCTPixel(x, y, &arr[2])
					arr4[x+y*8] = inverseDCTPixel(x, y, &arr[3])
					arr5[x+y*8] = inverseDCTPixel(x, y, &arr[4])
					arr6[x+y*8] = inverseDCTPixel(x, y, &arr[5])
				}
			}

			// Reset the values
			index := 0
			for k := 0; k < 8; k++ {
				base := 16 * k
				for j := base; j < base+8; j++ {
					(*mcu).ch1[j] = arr1[index]
					(*mcu).ch2[j] = arr2[index]
					(*mcu).ch3[j] = arr3[index]
					(*mcu).ch1[j+8] = arr4[index]
					(*mcu).ch2[j+8] = arr4[index]
					(*mcu).ch3[j+8] = arr6[index]
					index++
				}
			}
		case _8x16:
			arr1 := [64]int{}
			arr2 := [64]int{}
			arr3 := [64]int{}
			arr4 := [64]int{}
			arr5 := [64]int{}
			arr6 := [64]int{}

			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					arr1[x+8*y] = inverseDCTPixel(x, y, &arr[0])
					arr2[x+8*y] = inverseDCTPixel(x, y, &arr[1])
					arr3[x+8*y] = inverseDCTPixel(x, y, &arr[2])
					arr4[x+8*y] = inverseDCTPixel(x, y, &arr[3])
					arr5[x+8*y] = inverseDCTPixel(x, y, &arr[4])
					arr6[x+8*y] = inverseDCTPixel(x, y, &arr[5])
				}
			}

			// Reset the values
			for k := 0; k < 64; k++ {
				(*mcu).ch1[k] = arr1[k]
				(*mcu).ch2[k] = arr2[k]
				(*mcu).ch3[k] = arr3[k]
				(*mcu).ch1[k+64] = arr4[k]
				(*mcu).ch2[k+64] = arr5[k]
				(*mcu).ch3[k+64] = arr6[k]
			}
		case _16x16:
			arr1 := [64]int{}
			arr2 := [64]int{}
			arr3 := [64]int{}
			arr4 := [64]int{}
			arr5 := [64]int{}
			arr6 := [64]int{}
			arr7 := [64]int{}
			arr8 := [64]int{}
			arr9 := [64]int{}
			arr10 := [64]int{}
			arr11 := [64]int{}
			arr12 := [64]int{}

			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					arr1[x+8*y] = inverseDCTPixel(x, y, &arr[0])
					arr2[x+8*y] = inverseDCTPixel(x, y, &arr[1])
					arr3[x+8*y] = inverseDCTPixel(x, y, &arr[2])
					arr4[x+8*y] = inverseDCTPixel(x, y, &arr[3])
					arr5[x+8*y] = inverseDCTPixel(x, y, &arr[4])
					arr6[x+8*y] = inverseDCTPixel(x, y, &arr[5])
					arr7[x+8*y] = inverseDCTPixel(x, y, &arr[6])
					arr8[x+8*y] = inverseDCTPixel(x, y, &arr[7])
					arr9[x+8*y] = inverseDCTPixel(x, y, &arr[8])
					arr10[x+8*y] = inverseDCTPixel(x, y, &arr[9])
					arr11[x+8*y] = inverseDCTPixel(x, y, &arr[10])
					arr12[x+8*y] = inverseDCTPixel(x, y, &arr[11])
				}
			}

			// Reset the values
			index := 0
			for k := 0; k < 8; k++ {
				base := 16 * k
				for j := base; j < base+8; j++ {
					(*mcu).ch1[j] = arr1[index]
					(*mcu).ch2[j] = arr2[index]
					(*mcu).ch3[j] = arr3[index]
					(*mcu).ch1[j+8] = arr4[index]
					(*mcu).ch2[j+8] = arr5[index]
					(*mcu).ch3[j+8] = arr6[index]
					(*mcu).ch1[j+128] = arr7[index]
					(*mcu).ch2[j+128] = arr8[index]
					(*mcu).ch3[j+128] = arr9[index]
					(*mcu).ch1[j+128+8] = arr10[index]
					(*mcu).ch2[j+128+8] = arr11[index]
					(*mcu).ch3[j+128+8] = arr12[index]
					index++
				}
			}
		}

	}
}

func dequantize(header *Header) {
	for a := 0; a < header.mcuCount; a++ {
		mcu := &(header.MCUArray)[a]
		switch header.mcuDimensions {
		case _8x8:
			tb := (*getQuantizationTable(header, 0)).table
			for k := 0; k < 64; k++ {
				(*mcu).ch1[k] *= int(tb[k])
			}
			tb = (*getQuantizationTable(header, 1)).table
			for k := 0; k < 64; k++ {
				(*mcu).ch2[k] *= int(tb[k])
			}
			tb = (*getQuantizationTable(header, 2)).table
			for k := 0; k < 64; k++ {
				(*mcu).ch3[k] *= int(tb[k])
			}
		case _16x8:
			// Y1
			tb := (*getQuantizationTable(header, 0)).table
			index := 0
			for k := 0; k < 8; k++ {
				base := 16 * k
				for j := base; j < base+8; j++ {
					(*mcu).ch1[j] *= int(tb[index])
					index++
				}
			}
			// Y2
			index = 0
			for k := 0; k < 8; k++ {
				base := 16*k + 8
				for j := base; j < base+8; j++ {
					(*mcu).ch1[j] *= int(tb[index])
					index++
				}
			}
			// Cb
			tb = (*getQuantizationTable(header, 1)).table
			for k := 0; k < 64; k++ {
				(*mcu).ch2[k] *= int(tb[k])
			}
			// Cr
			tb = (*getQuantizationTable(header, 2)).table
			for k := 0; k < 64; k++ {
				(*mcu).ch3[k] *= int(tb[k])
			}
		case _8x16:
			// Y1
			tb := (*getQuantizationTable(header, 0)).table
			for k := 0; k < 64; k++ {
				(*mcu).ch1[k] *= int(tb[k])
			}
			// Y2
			for k := 64; k < 128; k++ {
				(*mcu).ch1[k] *= int(tb[k-64])
			}
			// Cb
			tb = (*getQuantizationTable(header, 1)).table
			for k := 0; k < 64; k++ {
				(*mcu).ch2[k] *= int(tb[k])
			}
			// Cr
			tb = (*getQuantizationTable(header, 2)).table
			for k := 0; k < 64; k++ {
				(*mcu).ch3[k] *= int(tb[k])
			}
		case _16x16:
			// Y1
			tb := (*getQuantizationTable(header, 0)).table
			index := 0
			for k := 0; k < 8; k++ {
				base := 16 * k
				for j := base; j < base+8; j++ {
					(*mcu).ch1[j] *= int(tb[index])
					index++
				}
			}
			// Y2
			index = 0
			for k := 0; k < 8; k++ {
				base := 16*k + 8
				for j := base; j < base+8; j++ {
					(*mcu).ch1[j] *= int(tb[index])
					index++
				}
			}
			// Y3
			index = 0
			for k := 8; k < 16; k++ {
				base := 16 * k
				for j := base; j < base+8; j++ {
					(*mcu).ch1[j] *= int(tb[index])
					index++
				}
			}
			// Y4
			index = 0
			for k := 8; k < 16; k++ {
				base := 16*k + 8
				for j := base; j < base+8; j++ {
					(*mcu).ch1[j] *= int(tb[index])
					index++
				}
			}
			// Cb
			tb = (*getQuantizationTable(header, 1)).table
			for k := 0; k < 64; k++ {
				(*mcu).ch2[k] *= int(tb[k])
			}
			// Cr
			tb = (*getQuantizationTable(header, 2)).table
			for k := 0; k < 64; k++ {
				(*mcu).ch3[k] *= int(tb[k])
			}
		}
	}
}

func decodeMCUCoeffecients(header *Header, br BitReader) {
	prevDC := [3]int{0, 0, 0}
	skips := 0
	for a := 0; a < header.mcuCount; a++ {
		// Get a new MCU Object with the correct dimensions
		mcu := getMCU(header)
		switch header.mcuDimensions {
		case _8x8:
			for c := range header.cComponents {
				comp := header.cComponents[c]
				if comp.usedInScan {
					acHuffmanTable := getTable(header, false, comp.acHuffmanTableId)
					dcHuffmanTable := getTable(header, true, comp.dcHuffmanTableId)
					coeff := read64Coeffecients(header, &br, acHuffmanTable, dcHuffmanTable, &prevDC[c], &skips)
					switch c {
					case 0:
						for k := 0; k < 64; k++ {
							(*mcu).ch1[zmap.Map1[k]] = coeff[k]
						}

					case 1:
						for k := 0; k < 64; k++ {
							(*mcu).ch2[zmap.Map1[k]] = coeff[k]
						}

					case 2:
						for k := 0; k < 64; k++ {
							(*mcu).ch3[zmap.Map1[k]] = coeff[k]
						}
					}
				}
			}
		case _16x8:
			for c := 0; c < 4; c++ {
				// Y(0) Y(1) Cb Cr
				var acHuffmanTable *HuffmanTable
				var dcHuffmanTable *HuffmanTable
				switch c {
				case 0:
					acHuffmanTable = getTable(header, false, header.cComponents[0].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[0].dcHuffmanTableId)
				case 1:
					acHuffmanTable = getTable(header, false, header.cComponents[0].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[0].dcHuffmanTableId)
				case 2:
					acHuffmanTable = getTable(header, false, header.cComponents[1].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[1].dcHuffmanTableId)
				case 3:
					acHuffmanTable = getTable(header, false, header.cComponents[2].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[2].dcHuffmanTableId)
				}
				var _prevDc *int
				switch c {
				case 0:
					_prevDc = &prevDC[0]
				case 1:
					_prevDc = &prevDC[0]
				case 2:
					_prevDc = &prevDC[1]
				case 3:
					_prevDc = &prevDC[2]
				}
				coeff := read64Coeffecients(header, &br, acHuffmanTable, dcHuffmanTable, _prevDc, &skips)

				switch c {
				case 0:
					for k := 0; k < 64; k++ {
						(*mcu).ch1[zmap.Map2[k]] = coeff[k]
					}
				case 1:
					for k := 0; k < 64; k++ {
						(*mcu).ch1[zmap.Map2[k]+8] = coeff[k]
					}
				case 2:
					for k := 0; k < 64; k++ {
						(*mcu).ch2[zmap.Map1[k]] = coeff[k]
					}
				case 3:
					for k := 0; k < 64; k++ {
						(*mcu).ch3[zmap.Map1[k]] = coeff[k]
					}
				}
			}
		case _8x16:
			for c := 0; c < 4; c++ {
				// Y(0) Y(1) Cb Cr
				var acHuffmanTable *HuffmanTable
				var dcHuffmanTable *HuffmanTable
				switch c {
				case 0:
					acHuffmanTable = getTable(header, false, header.cComponents[0].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[0].dcHuffmanTableId)
				case 1:
					acHuffmanTable = getTable(header, false, header.cComponents[0].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[0].dcHuffmanTableId)
				case 2:
					acHuffmanTable = getTable(header, false, header.cComponents[1].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[1].dcHuffmanTableId)
				case 3:
					acHuffmanTable = getTable(header, false, header.cComponents[2].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[2].dcHuffmanTableId)
				}
				var _prevDC *int
				switch c {
				case 0:
					_prevDC = &prevDC[0]
				case 1:
					_prevDC = &prevDC[0]
				case 2:
					_prevDC = &prevDC[1]
				case 3:
					_prevDC = &prevDC[2]

				}
				coeff := read64Coeffecients(header, &br, acHuffmanTable, dcHuffmanTable, _prevDC, &skips)
				switch c {
				case 0:
					for k := 0; k < 64; k++ {
						(*mcu).ch1[zmap.Map1[k]] = coeff[k]
					}
				case 1:
					for k := 0; k < 64; k++ {
						(*mcu).ch1[zmap.Map1[k]+64] = coeff[k]
					}
				case 2:
					for k := 0; k < 64; k++ {
						(*mcu).ch2[zmap.Map1[k]] = coeff[k]
					}
				case 3:
					for k := 0; k < 64; k++ {
						(*mcu).ch3[zmap.Map1[k]] = coeff[k]
					}
				}
			}
		case _16x16:
			for c := 0; c < 6; c++ {
				// Y(0) Y(1) Y(2) Y(3) Cb Cr
				var acHuffmanTable *HuffmanTable
				var dcHuffmanTable *HuffmanTable
				switch c {
				case 0:
					acHuffmanTable = getTable(header, false, header.cComponents[0].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[0].dcHuffmanTableId)
				case 1:
					acHuffmanTable = getTable(header, false, header.cComponents[0].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[0].dcHuffmanTableId)
				case 2:
					acHuffmanTable = getTable(header, false, header.cComponents[0].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[0].dcHuffmanTableId)
				case 3:
					acHuffmanTable = getTable(header, false, header.cComponents[0].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[0].dcHuffmanTableId)
				case 4:
					acHuffmanTable = getTable(header, false, header.cComponents[1].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[1].dcHuffmanTableId)
				case 5:
					acHuffmanTable = getTable(header, false, header.cComponents[2].acHuffmanTableId)
					dcHuffmanTable = getTable(header, true, header.cComponents[2].dcHuffmanTableId)
				}
				var _prevDC *int
				switch c {
				case 0:
					_prevDC = &prevDC[0]
				case 1:
					_prevDC = &prevDC[0]
				case 2:
					_prevDC = &prevDC[0]
				case 3:
					_prevDC = &prevDC[0]
				case 4:
					_prevDC = &prevDC[1]
				case 5:
					_prevDC = &prevDC[2]
				}
				coeff := read64Coeffecients(header, &br, acHuffmanTable, dcHuffmanTable, _prevDC, &skips)
				switch c {
				case 0:
					for k := 0; k < 64; k++ {
						(*mcu).ch1[zmap.Map2[k]] = coeff[k]
					}
				case 1:
					for k := 0; k < 64; k++ {
						(*mcu).ch1[zmap.Map2[k]+8] = coeff[k]
					}
				case 2:
					for k := 0; k < 64; k++ {
						(*mcu).ch1[zmap.Map2[k]+128] = coeff[k]
					}
				case 3:
					for k := 0; k < 64; k++ {
						(*mcu).ch1[zmap.Map2[k]+128+8] = coeff[k]
					}
				case 4:
					for k := 0; k < 64; k++ {
						(*mcu).ch2[zmap.Map2[k]] = coeff[k]
					}
				case 5:
					for k := 0; k < 64; k++ {
						(*mcu).ch3[zmap.Map2[k]] = coeff[k]
					}
				}
			}
		}
		// Add here
		header.MCUArray = append(header.MCUArray, *mcu)
	}
}

func decodeDefineRestartInterval(header *Header) {
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
	// Set the value of newInScan for all current tables = false
	for t := range header.huffmanTables {
		header.huffmanTables[t].newInScan = false
	}
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
			Id:        tableId,
			dc:        dc,
			newInScan: true,
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
		// For progressive JPGs there are new huffman-tables, thus check for tables that have the same id
		_newTables := []HuffmanTable{}
		for t := range header.huffmanTables {
			tb := header.huffmanTables[t]
			if tb.dc == dc && tb.Id == tableId {
				// Remove the (ac/dc) table with the same id as the new table
				continue
			}
			_newTables = append(_newTables, tb)
		}
		// Add the new table which replaces the previous table that had the same id
		_newTables = append(_newTables, table)
		header.huffmanTables = _newTables
	}
	if length != 0 {
		fmt.Printf("Error! Invalid DefineHuffanTable Marker\n")
	}
}

func endScan(header *Header) {
	buf := header.buffer
	fmt.Printf("*** EOI (0xFF%X) ***\n", buf.bf[0])
}

// Helper function to print the scan information
func printScanInfo(header *Header) {
	fmt.Printf("*** SCAN ***\n")
	fmt.Printf("** Huffman Tables (%d) **\n", len(header.huffmanTables))
	for t := range header.huffmanTables {
		tb := header.huffmanTables[t]
		if tb.newInScan {
			fmt.Printf("table id: %d ", tb.Id)
			if tb.dc {
				fmt.Printf("DC")
			} else {
				fmt.Printf("AC")
			}
			fmt.Printf("\n")
			fmt.Printf("Start Of Selection           : %d\n", header.startOfSelection)
			fmt.Printf("End Of Selection             : %d\n", header.endOfSelection)
			fmt.Printf("Succesive Approximation High : %d\n", header.successiveApproximationHigh)
			fmt.Printf("Succesive Approximation Low  : %d\n", header.successiveApproximationLow)
			fmt.Printf("# of components              : %d\n", header.componentsInScan)
			if false {
				fmt.Printf("--- Symbols ---\n")
				lastIndex := 0

				for a := byte(0); a < 16; a++ {
					fmt.Printf("%s -> ", pad(a))
					codesOfLen := tb.codesOfLen[int(a)]
					for c := lastIndex; c < lastIndex+codesOfLen; c++ {
						fmt.Printf("%x ", tb.symbols[c])
					}
					lastIndex += codesOfLen
					fmt.Printf("\n")
				}
			}
			fmt.Printf("\n")
			if false {
				fmt.Printf("--- Codes ---\n")
				lastIndex := 0
				for a := byte(0); a < 16; a++ {
					fmt.Printf("LEN (%d)\n", a)
					codesOfLen := tb.codesOfLen[int(a)]
					for c := lastIndex; c < lastIndex+codesOfLen; c++ {
						fmt.Printf("%b\n", tb.codes[c])
					}
					fmt.Printf("\n")
					lastIndex += codesOfLen
				}
				fmt.Printf("\n")
			}
		}
	}
}

func decodeStartOfScan(header *Header) {
	// Set the usedInScan prop of all components to false
	for c := range header.cComponents {
		header.cComponents[c].usedInScan = false
	}
	buf := header.buffer
	fmt.Printf("** Decoding Start of Scan (0xFF%X) **\n", buf.bf[0])
	buf.advance()
	buf.advance()
	length := (int(buf.bf[1]) << 8) + int(buf.bf[0]) - 2
	buf.advance()
	length -= 1
	components := int(buf.bf[0])
	// Set header.componentsInScan
	header.componentsInScan = components
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
				comp.usedInScan = true
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
	// The ECS provided by the current scan
	_bitstream := []byte{}
	// This loop should only get the ECS and break when we encounter a valid marker
	for {
		if buf.bf[0] == 0xFF {
			buf.advance()
			if buf.bf[0] == 0xFF {
				buf.advance()
				continue
			} else if buf.bf[0] >= RST0 && buf.bf[0] <= RST7 {
				buf.advance()
			} else if buf.bf[0] == EOI {
				break
			} else if buf.bf[0] == DRI && header.frameType == SOF2 {
				break
			} else if buf.bf[0] == DHT && header.frameType == SOF2 {
				break
			} else if buf.bf[0] == SOS && header.frameType == SOF2 {
				break
			} else if buf.bf[0] == 0x00 {
				// If one or more than one '0xff' bytes is followed by '0x00' then save a single '0xff'
				_bitstream = append(_bitstream, 0xff)
				buf.advance()
			} else {
				fmt.Printf("Invalid marker (0xFF%X) found in the bitsteam\n", buf.bf[0])
				os.Exit(1)
			}
		}
		_bitstream = append(_bitstream, buf.bf[0])
		buf.advance()
	}
	// Generate huffman codes for all the huffman tables
	for t := range header.huffmanTables {
		tb := &header.huffmanTables[t]
		generateCodes(tb)
	}
	// Print the length of the bitstream
	fmt.Printf("len(bitstream) = %d\n", len(_bitstream))
	// Print the scan info
	printScanInfo(header)
	// Decode the MCU Coeffecients
	decodeMCUCoeffecients(header, BitReader{data: &_bitstream})
	// Continue reading the other markers
	for {
		if buf.bf[0] == 0xff {
			buf.advance()
			continue
		}
		// Check for markers
		if buf.bf[0] == DRI && header.frameType == SOF2 {
			decodeDefineRestartInterval(header)
			buf.advance()
		}
		if buf.bf[0] == SOS && header.frameType == SOF2 {
			decodeStartOfScan(header)
			break
		}
		if buf.bf[0] == DHT && header.frameType == SOF2 {
			decodeDefineHuffmanTable(header)
			buf.advance()
		}
		if buf.bf[0] == EOI {
			fmt.Printf("*** Reached the end-of-image marker\n")
			break
		}
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
		} else if buffer.bf[0] == SOF2 {
			decodeStartOfFrame(header)
		} else if buffer.bf[0] == DRI {
			decodeDefineRestartInterval(header)
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

func YCbCrToRGB(header *Header) {
	mcuWidth := 0
	mcuHeight := 0
	switch header.mcuDimensions {
	case _8x8:
		mcuWidth = 8
		mcuHeight = 8
	case _16x8:
		mcuWidth = 16
		mcuHeight = 8
	case _8x16:
		mcuWidth = 8
		mcuHeight = 16
	case _16x16:
		mcuWidth = 16
		mcuHeight = 16
	}

	for a := 0; a < header.mcuCount; a++ {
		mcu := &header.MCUArray[a]
		for cf := 0; cf < mcuWidth*mcuHeight; cf++ {
			cY := &(*mcu).ch1[cf]
			cCb := &(*mcu).ch2[cf]
			cCr := &(*mcu).ch3[cf]
			cR := float32((*cY)) + (1.402 * (float32(*cCr))) + 128
			cG := float32((*cY)) - (0.344 * (float32(*cCb))) - (0.714 * float32((*cCr))) + 128
			cB := float32((*cY)) + (1.772 * (float32(*cCb))) + 128
			if cR < 0 {
				cR = 0
			}
			if cR > 255 {
				cR = 255
			}
			if cB < 0 {
				cB = 0
			}
			if cB > 255 {
				cB = 255
			}
			if cG < 0 {
				cG = 0
			}
			if cG > 255 {
				cG = 255
			}
			*cY = int(cR)
			*cCb = int(cG)
			*cCr = int(cB)
		}
	}
}

func generateCodes(tb *HuffmanTable) {
	// Iterate through all the lenghts
	code := 0
	lastIndex := 0
	for a := 0; a < 16; a++ {
		nCodes := tb.codesOfLen[a]
		for k := lastIndex; k < lastIndex+nCodes; k++ {
			tb.codes = append(tb.codes, code)
			code += 1
		}
		code <<= 1
		lastIndex += nCodes
	}
}

func spreadMCU(header *Header) {
	for a := 0; a < header.mcuCount; a++ {
		mcu := &header.MCUArray[a]
		arr := mcu.getArraySections(header)
		switch header.mcuDimensions {
		case _16x8:
			for k := 0; k < 64; k++ {
				// Cb
				(*mcu).ch2[2*k] = arr[1][k]
				(*mcu).ch2[2*k+1] = arr[1][k]
				// Cr
				(*mcu).ch3[2*k] = arr[2][k]
				(*mcu).ch3[2*k+1] = arr[2][k]
			}
		case _8x16:
			index := 0
			for k := 0; k < 8; k++ {
				base := 16 * k
				for j := base; j < base+8; j++ {
					// Cb
					(*mcu).ch2[j] = arr[1][index]
					(*mcu).ch2[j+8] = arr[1][index]
					// Cr
					(*mcu).ch3[j] = arr[2][index]
					(*mcu).ch3[j+8] = arr[2][index]
					index++
				}
			}
		case _16x16:
			// Handle vertical
			index := 0
			for k := 0; k < 8; k++ {
				base := 32 * k
				for b := base; b < base+16; b++ {
					if b%2 == 0 {
						// Cb
						(*mcu).ch2[b] = arr[1][index]
						(*mcu).ch2[b+16] = arr[1][index]
						(*mcu).ch2[b+1] = arr[1][index]
						(*mcu).ch2[b+16+1] = arr[1][index]
						// Cr
						(*mcu).ch3[b] = arr[2][index]
						(*mcu).ch3[b+16] = arr[2][index]
						(*mcu).ch3[b+1] = arr[2][index]
						(*mcu).ch3[b+16+1] = arr[2][index]
						index++
					}
				}
			}
		}
	}
}

func writeBitMap(header *Header) {
	paddingSize := header.width % 4
	// The total size
	// 14 -> The first header
	// 12 -> The second header
	// the total number of (pixels * 3) bytes (1 byte per pixel)
	// the total paddding bytes
	size := 14 + 12 + (header.height * header.width * 3) + (paddingSize * header.height)
	// Create the file
	filename := path.Base(header.filename)
	i := strings.LastIndex(filename, ".")
	filename = filename[:i]
	filename += ".bmp"
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Error! %s\n", err.Error())
		os.Exit(1)
	}
	fmt.Printf("Writing bitmap to %s ... \n", filename)
	// Write 'B' 'M'
	f.Write([]byte("BM"))  // BM
	put4Int(uint(size), f) // The size of the file as a 4 byte integer
	put4Int(uint(0), f)    // 4 zeros as 4 byte integer
	put4Int(uint(26), f)   // The pixel array offset as a 4 byte integer
	// The DIB Header
	put4Int(12, f)                  // The size of the DIB header as a 4 byte integer
	put2Int(uint(header.width), f)  // The height as a 2 byte integer
	put2Int(uint(header.height), f) // The width as a 2 byte integer
	put2Int(uint(1), f)             // The number of planes as 2 bit integer
	put2Int(uint(24), f)            // The number of bits per pixel as 2 bit integer

	maxWidth := 0
	maxHeight := 0
	switch header.mcuDimensions {
	case _8x8:
		maxWidth = 8
		maxHeight = 8
	case _16x8:
		maxWidth = 16
		maxHeight = 8
	case _8x16:
		maxWidth = 8
		maxHeight = 16
	case _16x16:
		maxWidth = 16
		maxHeight = 16
	}

	for y := header.height - 1; y >= 0; y-- {
		_mcuY := y % maxHeight
		_mcuRow := y / maxHeight
		for x := 0; x < header.width; x++ {
			_mcuX := x % maxWidth
			_mcuColumn := x / maxWidth
			_mcuIndex := _mcuColumn + header.mcuWidth*_mcuRow
			_pixelIndex := _mcuX + maxWidth*_mcuY
			// Write the RGB Values
			rgb := []byte{}
			rgb = append(rgb, byte(header.MCUArray[_mcuIndex].ch3[_pixelIndex]))
			rgb = append(rgb, byte(header.MCUArray[_mcuIndex].ch2[_pixelIndex]))
			rgb = append(rgb, byte(header.MCUArray[_mcuIndex].ch1[_pixelIndex]))
			f.Write(rgb)
		}
		padding := []byte{}
		padding = make([]byte, paddingSize)
		f.Write(padding)
	}
	f.Close()
}

// Helper function to write a 4 byte integer in little endian
func put4Int(a uint, f *os.File) {
	data := []byte{}
	data = make([]byte, 4)
	data[0] = byte((a >> 0) & 0xFF)
	data[1] = byte((a >> 8) & 0xFF)
	data[2] = byte((a >> 16) & 0xFF)
	data[3] = byte((a >> 24) & 0xFF)
	_, err := f.Write(data)
	if err != nil {
		fmt.Printf("Error! %s\n", err.Error())
		os.Exit(1)
	}
}

// Helper function to write a 2 byte integer in little endian
func put2Int(a uint, f *os.File) {
	data := []byte{}
	data = make([]byte, 2)
	data[0] = byte((a >> 0) & 0xFF)
	data[1] = byte((a >> 8) & 0xFF)
	_, err := f.Write(data)
	if err != nil {
		fmt.Printf("Error! %s\n", err.Error())
		os.Exit(1)
	}
}

func decodeChannelData(br *BitReader, channel *[64]int, acHuffmanTable *HuffmanTable, dcHuffmanTable *HuffmanTable, prevDC *int) {
	// Decode the DC coeffecient
	sym := scanSymbol(br, dcHuffmanTable)
	if sym == 0xFF {
		fmt.Printf("Error! invalid DC Symbols\n")
		os.Exit(1)
	}
	dcLength := int(sym)
	// Since for DC length == sym
	coeff := br.readBits(dcLength)
	if dcLength != 0 && coeff < (1<<(dcLength-1)) {
		coeff -= ((1 << dcLength) - 1)
	}
	coeff += *prevDC
	*prevDC = coeff
	(*channel)[0] = coeff
	// Decode the AC Coeffecients
	index := 1
	for {
		if index > 63 {
			break
		}
		sym = scanSymbol(br, acHuffmanTable)
		switch sym {
		// The remaining coeffecients are all 0
		case 0x00:
			for a := index; a <= 63; a++ {
				(*channel)[zigzag[a]] = 0
				index++
			}
		// The next 16 coeffecients are all 0
		case 0xF0:
			max := index + 16
			for a := index; a < max; a++ {
				(*channel)[zigzag[a]] = 0
				index++
			}
		// Decode the coeffLength and numZeros
		default:
			numZeros := sym >> 4
			coeffLength := int(sym & 0x0F)
			max := index + int(numZeros)
			for a := index; a < max; a++ {
				(*channel)[zigzag[a]] = 0
				index++
			}
			// read the coeffecient
			coeff := br.readBits(int(coeffLength))
			if coeff < (1 << (coeffLength - 1)) {
				coeff -= ((1 << coeffLength) - 1)
			}
			(*channel)[zigzag[index]] = coeff
			index++
		}
	}
}

// An MCU is a 1d array whose size depends on the scaling factor
type MCU struct {
	ch1 []int
	ch2 []int
	ch3 []int
}

func getMCU(header *Header) *MCU {
	switch header.mcuDimensions {
	case _8x8:
		return &MCU{make([]int, 64), make([]int, 64), make([]int, 64)}
	case _8x16:
		return &MCU{make([]int, 128), make([]int, 128), make([]int, 128)}
	case _16x8:
		return &MCU{make([]int, 128), make([]int, 128), make([]int, 128)}
	case _16x16:
		return &MCU{make([]int, 256), make([]int, 256), make([]int, 256)}
	}
	return nil
}

// Helper functions to get the [64]int from the MCU
func (mcu *MCU) getArraySections(header *Header) [][64]int {
	res := [][64]int{}
	switch header.mcuDimensions {
	case _8x8:
		res = make([][64]int, 3)
		for k := 0; k < 64; k++ {
			res[0][k] = (*mcu).ch1[k]
			res[1][k] = (*mcu).ch2[k]
			res[2][k] = (*mcu).ch3[k]
		}
		return res
	case _16x8:
		res = make([][64]int, 6)
		index := 0
		for a := 0; a < 8; a++ {
			base := 16 * a
			for k := base; k < base+8; k++ {
				res[0][index] = (*mcu).ch1[k]
				res[1][index] = (*mcu).ch2[k]
				res[2][index] = (*mcu).ch3[k]
				res[3][index] = (*mcu).ch1[k+8]
				res[4][index] = (*mcu).ch2[k+8]
				res[5][index] = (*mcu).ch3[k+8]
				index++
			}
		}
		return res
	case _8x16:
		res = make([][64]int, 6)
		for k := 0; k < 64; k++ {
			res[0][k] = (*mcu).ch1[k]
			res[1][k] = (*mcu).ch2[k]
			res[2][k] = (*mcu).ch3[k]
			res[3][k] = (*mcu).ch1[k+64]
			res[4][k] = (*mcu).ch2[k+64]
			res[5][k] = (*mcu).ch3[k+64]
		}
		return res
	case _16x16:
		res = make([][64]int, 12)
		index := 0
		for a := 0; a < 8; a++ {
			base := 16 * a
			for k := base; k < base+8; k++ {
				res[0][index] = (*mcu).ch1[k]
				res[1][index] = (*mcu).ch2[k]
				res[2][index] = (*mcu).ch3[k]
				res[3][index] = (*mcu).ch1[k+8]
				res[4][index] = (*mcu).ch2[k+8]
				res[5][index] = (*mcu).ch3[k+8]
				res[6][index] = (*mcu).ch1[k+128]
				res[7][index] = (*mcu).ch2[k+128]
				res[8][index] = (*mcu).ch3[k+128]
				res[9][index] = (*mcu).ch1[k+128+8]
				res[10][index] = (*mcu).ch2[k+128+8]
				res[11][index] = (*mcu).ch3[k+128+8]
				index++
			}
		}
		return res
	}
	return nil
}

type BitReader struct {
	data     *[]byte
	nextByte int
	nextBit  int
}

func (br *BitReader) align() {
	if br.nextByte >= len(*br.data) {
		return
	}
	if br.nextBit != 0 {
		br.nextBit = 0
		br.nextByte += 1
	}
}

// Helper function used to read individual bits
// reuturns -1 you try reading beyound the []data
func (br *BitReader) readBit() int {
	b := 0
	if br.nextByte >= len(*br.data) {
		return -1
	}
	b = (int((*br.data)[br.nextByte]) >> (7 - br.nextBit)) & 1
	br.nextBit++
	if br.nextBit == 8 {
		br.nextByte++
		br.nextBit = 0
	}
	return int(b)
}

func (br *BitReader) readBits(c int) int {
	bits := 0
	for a := 0; a < c; a++ {
		bit := br.readBit()
		if bit == -1 {
			return -1
		}
		bits = (bits << 1) | bit
	}
	return bits
}

func scanSymbol(br *BitReader, ht *HuffmanTable) byte {
	code := 0
	lastIndex := 0
	for a := 0; a < 16; a++ {
		bit := br.readBit()
		if bit == -1 {
			// 0xFF is not a valid symbols and thus can be used to detect errors
			return 0xFF
		}
		code = (code << 1) | bit
		nCodes := ht.codesOfLen[a]
		for k := lastIndex; k < lastIndex+nCodes; k++ {
			if code == ht.codes[k] {
				return ht.symbols[k]
			}
		}
		lastIndex += nCodes
	}
	return 0xFF
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

	fmt.Printf("** MCU --> width: (%d) height: (%d) count:(%d)\n", header.mcuWidth, header.mcuHeight, header.mcuCount)
	/**
	fmt.Printf("\n***** Codes *****\n\n")
	for t := range header.huffmanTables {
		tb := header.huffmanTables[t]
		var out bytes.Buffer
		if tb.dc {
			out.WriteString("* DC ")
		} else {
			out.WriteString("* AC ")
		}
		out.WriteString(fmt.Sprintf("Id #%d *\n", tb.Id))
		lastIndex := 0
		for a := 0; a < 16; a++ {
			out.WriteString(fmt.Sprintf("Len #%d\n", a+1))
			nCodes := tb.codesOfLen[a]
			for k := lastIndex; k < lastIndex+nCodes; k++ {
				out.WriteString(fmt.Sprintf("%b\n", tb.codes[k]))
			}
			lastIndex += nCodes
			out.WriteString("\n")
		}
		out.WriteString("\n")
		fmt.Printf(out.String())
	}
	**/
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
	codes      []int
	symbols    []byte
	codesOfLen [16]int
	dc         bool
	newInScan  bool // Is the table new from the most recset scan
}

// The mcu dimensions
const (
	_ = iota
	_8x8
	_8x16
	_16x8
	_16x16
)

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
	MCUArray                    []MCU
	mcuWidth                    int
	mcuHeight                   int
	mcuDimensions               int
	mcuCount                    int
	componentsInScan            int  // The numnber of components used in the scan
	frameType                   byte // SOF0 or SOF2
}

type ColorComponent struct {
	Id               int
	hSamplingFactor  int
	vSamplingFactor  int
	qTableId         int
	acHuffmanTableId int
	dcHuffmanTableId int
	usedInScan       bool // Is this component used in the scan
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
