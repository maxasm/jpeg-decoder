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
	data := make([]byte, 1)
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

	// blocks
	h.blockWidth = (h.width + 7) / 8
	h.blockHeight = (h.height + 7) / 8
	h.blockWidthReal = h.blockWidth
	h.blockHeightReal = h.blockHeight
	// the luminance channel determines the MCU dimensions
	comp := h.cComponents[0]
	if comp.vSamplingFactor == 2 && h.blockHeight%2 == 1 {
		h.blockHeightReal += 1
	}
	if comp.hSamplingFactor == 2 && h.blockWidth%2 == 1 {
		h.blockWidthReal += 1
	}
	h.blockCount = h.blockHeightReal * h.blockWidthReal
	_arr := make([]Block, h.blockCount)
	h.blocks = &_arr
	// Check if len == 0
	if length != 0 {
		fmt.Printf("Error! Invalid Start Of Frame\n")
	}

}

func decodeBandCoeffecients(header *Header, br *BitReader, acHuffmanTable *HuffmanTable, dcHuffmanTable *HuffmanTable, prevDC *int, skips *int, channel *[64]int) {
	// cmap for mapping coeffecients
	var cmap map[int]int = zmap.Map1

	if header.frameType == SOF0 {
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
					(*channel)[cmap[a]] = 0
					index++
				}
			// The next 16 coeffecients are all 0
			case 0xF0:
				max := index + 16
				for a := index; a < max; a++ {
					(*channel)[cmap[a]] = 0
					index++
				}
			// Decode the coeffLength and numZeros
			default:
				numZeros := sym >> 4
				coeffLength := int(sym & 0x0F)
				max := index + int(numZeros)
				for a := index; a < max; a++ {
					(*channel)[cmap[a]] = 0
					index++
				}
				// read the coeffecient
				coeff := br.readBits(int(coeffLength))
				if coeff < (1 << (coeffLength - 1)) {
					coeff -= ((1 << coeffLength) - 1)
				}
				(*channel)[cmap[index]] = coeff
				index++
			}
		}
	} else if header.frameType == SOF2 {
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
			(*channel)[cmap[0]] = dcCoeffecient << header.successiveApproximationLow
		} else if header.startOfSelection != 0 && header.successiveApproximationHigh == 0 {
			/** AC First Visit **/
			if *skips > 0 {
				*skips -= 1
				return
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
						(*channel)[cmap[a]] = 0
						index++
					}
				default:
					numZeros := int(sym >> 4)
					acLength := int(sym & 0x0F)
					if acLength != 0 {
						max := index + numZeros
						for a := index; a < max; a++ {
							(*channel)[cmap[a]] = 0
							index++
						}
						acCoeffecient := br.readBits(acLength)
						if acCoeffecient < (1 << (acLength - 1)) {
							acCoeffecient -= (1<<acLength - 1)
						}
						(*channel)[cmap[index]] = acCoeffecient << int(header.successiveApproximationLow)
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
						return
					}
				}
			}
		} else if header.startOfSelection == 0 && header.successiveApproximationHigh != 0 {
			// For DC refinement all you need to do is read a single bit,
			// shift it left by successiveApproximationHight, then bin-or it with the current DC coeffecient
			bit := br.readBit()
			if bit == 0xff {
				fmt.Printf("Error! invalid DC refinment bit read\n")
				os.Exit(1)
			}
			(*channel)[cmap[0]] |= bit << header.successiveApproximationLow
		} else if header.startOfSelection != 0 && header.successiveApproximationHigh != 0 {
			// negative and positie bits
			positive := 1 << header.successiveApproximationLow
			negative := -1 << header.successiveApproximationLow
			index := int(header.startOfSelection)

			if *skips == 0 {
				// Perform huffman-decoding, read a new bit for every non-zero coeffecient
				for {
					if index > int(header.endOfSelection) {
						break
					}
					sym := scanSymbol(br, acHuffmanTable)
					// check if the symbol is valid
					if sym == 0xff {
						fmt.Printf("Error! Invalid symbol 0xff\n")
						os.Exit(1)
					}
					// get the number of zeroes and the coeffecient lenght
					zeroes := sym >> 4
					coeffLen := sym & 0x0f
					// the coeffecient that will be set
					coeff := 0

					// coeffLen should be 1 because this is a refinment scan
					if coeffLen != 0 {
						if coeffLen != 1 {
							fmt.Printf("Error! Invalid coeffLen expected %d but got %d\n", 1, coeffLen)
							os.Exit(1)
						}
						bit := br.readBit()
						if bit == 1 {
							coeff = positive
						} else if bit == 0 {
							coeff = negative
						} else {
							fmt.Printf("Error\n")
							os.Exit(1)
						}
					}

					// check for end-of-band symbols
					if coeffLen == 0 && sym != 0xf0 {
						*skips = (1 << zeroes) + br.readBits(int(zeroes))
						// fmt.Printf("eob -> %d\n", *skips)
						break
					}
					// Handle the zeroes
					for {
						var currCoeff *int
						currCoeff = &((*channel)[cmap[index]])
						// read a new bit for every non-zero coeffecient
						if *currCoeff != 0 {
							bit := br.readBit()
							if bit == 1 {
								if *currCoeff >= 0 {
									*currCoeff += positive
								} else {
									*currCoeff += negative
								}
							} else if bit == 0 {
								// do nothing
							} else {
								fmt.Printf("Error bit -> %d\n", bit)
								os.Exit(1)
							}
						} else {
							if zeroes == 0 {
								break
							}
							zeroes -= 1
						}
						index += 1
					}
					(*channel)[cmap[index]] = coeff
					index += 1
				}
			}

			if *skips > 0 {
				for {
					if index > int(header.endOfSelection) {
						break
					}
					var currCoeff *int
					currCoeff = &(*channel)[cmap[index]]
					// read a new bit for every non-zero coeffeceint
					if *currCoeff != 0 {
						bit := br.readBit()
						if bit == 1 {
							if *currCoeff >= 0 {
								*currCoeff += positive
							} else {
								*currCoeff += negative
							}
						} else if bit == 0 {
							// do nothing
						} else {
							fmt.Printf("Error bit 2-> %d\n", bit)
							os.Exit(1)
						}
					}
					index += 1
				}
				*skips -= 1
			}
		}
	}
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
func getQuantizationTable(header *Header, compIndex int) *QuantizationTable {
	tId := header.cComponents[compIndex].qTableId
	for a := range header.qTables {
		t := header.qTables[a]
		if t.Id == tId {
			return &t
		}
	}
	return nil
}

func inverseDCTOnComponent(chann *[64]int) {
	// 1D IDCT on Columns
	for i := 0; i < 8; i++ {
		// g
		var g0 float64 = float64((*chann)[i]) * S0
		var g1 float64 = float64((*chann)[4*8+i]) * S4
		var g2 float64 = float64((*chann)[2*8+i]) * S2
		var g3 float64 = float64((*chann)[6*8+i]) * S6
		var g4 float64 = float64((*chann)[5*8+i]) * S5
		var g5 float64 = float64((*chann)[1*8+i]) * S1
		var g6 float64 = float64((*chann)[7*8+i]) * S7
		var g7 float64 = float64((*chann)[3*8+i]) * S3

		// f
		var f0 float64 = g0
		var f1 float64 = g1
		var f2 float64 = g2
		var f3 float64 = g3
		var f4 float64 = g4 - g7
		var f5 float64 = g5 + g6
		var f6 float64 = g5 - g6
		var f7 float64 = g4 + g7

		// e
		var e0 float64 = f0
		var e1 float64 = f1
		var e2 float64 = f2 - f3
		var e3 float64 = f2 + f3
		var e4 float64 = f4
		var e5 float64 = f5 - f7
		var e6 float64 = f6
		var e7 float64 = f5 + f7
		var e8 float64 = f4 + f6

		// d
		var d0 float64 = e0
		var d1 float64 = e1
		var d2 float64 = e2 * M1
		var d3 float64 = e3
		var d4 float64 = e4 * M2
		var d5 float64 = e5 * M3
		var d6 float64 = e6 * M4
		var d7 float64 = e7
		var d8 float64 = e8 * M5

		// c
		var c0 float64 = d0 + d1
		var c1 float64 = d0 - d1
		var c2 float64 = d2 - d3
		var c3 float64 = d3
		var c4 float64 = d4 + d8
		var c5 float64 = d5 + d7
		var c6 float64 = d6 - d8
		var c7 float64 = d7
		var c8 float64 = c5 - c6

		// b
		var b0 float64 = c0 + c3
		var b1 float64 = c1 + c2
		var b2 float64 = c1 - c2
		var b3 float64 = c0 - c3
		var b4 float64 = c4 - c8
		var b5 float64 = c8
		var b6 float64 = c6 - c7
		var b7 float64 = c7

		// a -> final output
		(*chann)[i] = int(b0 + b7)
		(*chann)[1*8+i] = int(b1 + b6)
		(*chann)[2*8+i] = int(b2 + b5)
		(*chann)[3*8+i] = int(b3 + b4)
		(*chann)[4*8+i] = int(b3 - b4)
		(*chann)[5*8+i] = int(b2 - b5)
		(*chann)[6*8+i] = int(b1 - b6)
		(*chann)[7*8+i] = int(b0 - b7)
	}

	// 1D IDCT On Rows
	for i := 0; i < 8; i++ {
		// g
		var g0 float64 = float64((*chann)[i*8+0]) * S0
		var g1 float64 = float64((*chann)[i*8+4]) * S4
		var g2 float64 = float64((*chann)[i*8+2]) * S2
		var g3 float64 = float64((*chann)[i*8+6]) * S6
		var g4 float64 = float64((*chann)[i*8+5]) * S5
		var g5 float64 = float64((*chann)[i*8+1]) * S1
		var g6 float64 = float64((*chann)[i*8+7]) * S7
		var g7 float64 = float64((*chann)[i*8+3]) * S3

		// f
		var f0 float64 = g0
		var f1 float64 = g1
		var f2 float64 = g2
		var f3 float64 = g3
		var f4 float64 = g4 - g7
		var f5 float64 = g5 + g6
		var f6 float64 = g5 - g6
		var f7 float64 = g4 + g7

		// e
		var e0 float64 = f0
		var e1 float64 = f1
		var e2 float64 = f2 - f3
		var e3 float64 = f2 + f3
		var e4 float64 = f4
		var e5 float64 = f5 - f7
		var e6 float64 = f6
		var e7 float64 = f5 + f7
		var e8 float64 = f4 + f6

		// d
		var d0 float64 = e0
		var d1 float64 = e1
		var d2 float64 = e2 * M1
		var d3 float64 = e3
		var d4 float64 = e4 * M2
		var d5 float64 = e5 * M3
		var d6 float64 = e6 * M4
		var d7 float64 = e7
		var d8 float64 = e8 * M5

		// c
		var c0 float64 = d0 + d1
		var c1 float64 = d0 - d1
		var c2 float64 = d2 - d3
		var c3 float64 = d3
		var c4 float64 = d4 + d8
		var c5 float64 = d5 + d7
		var c6 float64 = d6 - d8
		var c7 float64 = d7
		var c8 float64 = c5 - c6

		// b
		var b0 float64 = c0 + c3
		var b1 float64 = c1 + c2
		var b2 float64 = c1 - c2
		var b3 float64 = c0 - c3
		var b4 float64 = c4 - c8
		var b5 float64 = c8
		var b6 float64 = c6 - c7
		var b7 float64 = c7

		// a -> final output
		(*chann)[i*8+0] = int(b0 + b7)
		(*chann)[i*8+1] = int(b1 + b6)
		(*chann)[i*8+2] = int(b2 + b5)
		(*chann)[i*8+3] = int(b3 + b4)
		(*chann)[i*8+4] = int(b3 - b4)
		(*chann)[i*8+5] = int(b2 - b5)
		(*chann)[i*8+6] = int(b1 - b6)
		(*chann)[i*8+7] = int(b0 - b7)
	}
}

// Inverse DCT
func inverseDCT(header *Header) {
	for y := 0; y < header.blockHeightReal; y++ {
		for x := 0; x < header.blockWidthReal; x++ {
			blockIndex := x + y*header.blockWidthReal
			block := &(*header.blocks)[blockIndex]
			for cp := range header.cComponents {
				//comp := header.cComponents[cp]
				var chann *[64]int
				switch cp {
				case 0:
					chann = &(*block).ch1
				case 1:
					chann = &(*block).ch2
				case 2:
					chann = &(*block).ch3
				default:
					chann = nil
				}
				if chann == nil {
					fmt.Printf("Error! chan = nil\n")
					os.Exit(1)
				}
				inverseDCTOnComponent(chann)
			}
		}
	}
}

// dequntize the coeffecients
func dequantize(header *Header) {
	for y := 0; y < header.blockHeightReal; y++ {
		for x := 0; x < header.blockWidthReal; x++ {
			blockIndex := x + y*header.blockWidthReal
			block := &(*header.blocks)[blockIndex]
			for cp := range header.cComponents {
				var chann *[64]int
				switch cp {
				case 0:
					chann = &(*block).ch1
				case 1:
					chann = &(*block).ch2
				case 2:
					chann = &(*block).ch3
				default:
					chann = nil
				}
				if chann == nil {
					fmt.Printf("Error! chann = nil\n")
					os.Exit(1)
				}
				tb := getQuantizationTable(header, cp)
				for i := 0; i < 64; i++ {
					(*chann)[i] *= int((*tb).table[i])
				}
			}
		}
	}
}

// YCbCr -> RGB
func convertColorSpace(header *Header) {
	for y := 0; y < header.blockHeightReal; y++ {
		for x := 0; x < header.blockWidthReal; x++ {
			block := &(*header.blocks)[x+y*header.blockWidthReal]
			for a := 0; a < 64; a++ {
				// YCbCr
				Y := &(*block).ch1[a]
				cb := &(*block).ch2[a]
				cr := &(*block).ch3[a]
				// RGB
				r := float32((*Y)) + (1.402 * (float32(*cr))) + 128
				g := float32((*Y)) - (0.344 * (float32(*cb))) - (0.714 * float32((*cr))) + 128
				b := float32((*Y)) + (1.772 * (float32(*cb))) + 128
				if r < 0 {
					r = 0
				}
				if r > 255 {
					r = 255
				}
				if b < 0 {
					b = 0
				}
				if b > 255 {
					b = 255
				}
				if g < 0 {
					g = 0
				}
				if g > 255 {
					g = 255
				}
				// set the 'rgb' values
				*Y = int(r)
				*cb = int(g)
				*cr = int(b)
			}
		}
	}
}

// spread coeffecient values
func spreadCoeffecients(header *Header) {
	yStep := header.cComponents[0].vSamplingFactor
	xStep := header.cComponents[0].hSamplingFactor

	for y := 0; y < header.blockHeight; y += yStep {
		for x := 0; x < header.blockWidth; x += xStep {
			// rBlock contains all the coeffecients that we need for the cb and cr
			rBlock := (*header.blocks)[x+y*header.blockWidthReal]
			for py := 0; py < 8*yStep; py += yStep {
				yBlock := py / 8
				for px := 0; px < 8*xStep; px += xStep {
					xBlock := px / 8
					// cBlock is the block where the coeffecient data is being writen to
					cBlock := &(*header.blocks)[(x+xBlock)+(y+yBlock)*header.blockWidthReal]
					// the index of the coeffecients that we are copying from the refference block
					rYIndex := py / 2
					rXIndex := px / 2
					// the index of the coffecients that we are writing to
					cYIndex := py
					cXIndex := px
					if cYIndex >= 8 {
						cYIndex %= 8
					}
					if cXIndex >= 8 {
						cXIndex %= 8
					}
					// set the values
					for u := 0; u < yStep; u++ {
						for v := 0; v < xStep; v++ {
							(*cBlock).ch2[(cXIndex+v)+8*(cYIndex+u)] = rBlock.ch2[rXIndex+8*rYIndex]
							(*cBlock).ch3[(cXIndex+v)+8*(cYIndex+u)] = rBlock.ch3[rXIndex+8*rYIndex]
						}
					}
				}
			}
		}
	}
}

func decodeHuffmanData(header *Header, br *BitReader) {
	prevDC := [3]int{0, 0, 0}
	skips := 0

	luminanceOnlyScan := false
	if header.componentsInScan == 1 && header.cComponents[0].usedInScan {
		luminanceOnlyScan = true
	}

	var xStep int
	var yStep int

	if luminanceOnlyScan {
		xStep = 1
		yStep = 1
	} else {
		xStep = header.cComponents[0].hSamplingFactor
		yStep = header.cComponents[0].vSamplingFactor
	}

	for y := 0; y < header.blockHeight; y += yStep {
		for x := 0; x < header.blockWidth; x += xStep {
			for cp := range header.cComponents {
				comp := header.cComponents[cp]
				acHuffmanTable := getTable(header, false, comp.acHuffmanTableId)
				dcHuffmanTable := getTable(header, true, comp.dcHuffmanTableId)
				if comp.usedInScan {
					var xMax int
					var yMax int
					if luminanceOnlyScan {
						yMax = 1
						xMax = 1
					} else {
						yMax = comp.vSamplingFactor
						xMax = comp.hSamplingFactor
					}
					for u := 0; u < yMax; u++ {
						for v := 0; v < xMax; v++ {
							blockIndex := (x + v) + (y+u)*header.blockWidthReal
							block := &(*header.blocks)[blockIndex]
							var chann *[64]int
							switch cp {
							case 0:
								chann = &(*block).ch1
							case 1:
								chann = &(*block).ch2
							case 2:
								chann = &(*block).ch3
							default:
								chann = nil
							}
							// decode the coeffecients in the band
							decodeBandCoeffecients(
								header,
								br,
								acHuffmanTable,
								dcHuffmanTable,
								&prevDC[cp],
								&skips,
								chann,
							)
						}
					}
				}
			}
		}
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
					fmt.Printf("%s -> ", pad(int(a)))
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
		} else {
			_bitstream = append(_bitstream, buf.bf[0])
			buf.advance()
		}
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
	// Decode the Coeffecients
	decodeHuffmanData(header, &BitReader{data: &_bitstream})
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
			dequantize(header)
			inverseDCT(header)
			spreadCoeffecients(header)
			convertColorSpace(header)
			writeBitMap(header)
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

func writeBitMap(header *Header) {
	paddingSize := header.width % 4
	size := 14 + 12 + (header.height * header.width * 3) + (paddingSize * header.height)
	// Create the file
	//filename := header.filename
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

	for y := header.height - 1; y >= 0; y-- {
		blockRow := y / 8
		pixelRow := y % 8
		for x := 0; x < header.width; x++ {
			blockColumn := x / 8
			pixelColumn := x % 8
			blockIndex := blockColumn + blockRow*header.blockWidthReal
			pixelIndex := pixelColumn + pixelRow*8
			// write the 'rgb' values
			block := (*header.blocks)[blockIndex]
			rgbData := []byte{}
			rgbData = append(rgbData, byte(block.ch3[pixelIndex]))
			rgbData = append(rgbData, byte(block.ch2[pixelIndex]))
			rgbData = append(rgbData, byte(block.ch1[pixelIndex]))
			f.Write(rgbData)
		}
		padding := make([]byte, paddingSize)
		f.Write(padding)
	}
	f.Close()
}

// Helper function to write a 4 byte integer in little endian
func put4Int(a uint, f *os.File) {
	data := make([]byte, 4)
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
	data := make([]byte, 2)
	data[0] = byte((a >> 0) & 0xFF)
	data[1] = byte((a >> 8) & 0xFF)
	_, err := f.Write(data)
	if err != nil {
		fmt.Printf("Error! %s\n", err.Error())
		os.Exit(1)
	}
}

type Block struct {
	ch1 [64]int
	ch2 [64]int
	ch3 [64]int
}

type BitReader struct {
	data     *[]byte
	nextByte int
	nextBit  int
}

// Todo: Handle restart interval
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

func pad(bt int) string {
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

// M-Factors
var M0 float64 = 2.0 * math.Cos(1.0/16.0*2.0*math.Pi)
var M1 float64 = 2.0 * math.Cos(2.0/16.0*2.0*math.Pi)
var M3 float64 = 2.0 * math.Cos(2.0/16.0*2.0*math.Pi)
var M5 float64 = 2.0 * math.Cos(3.0/16.0*2.0*math.Pi)
var M2 float64 = M0 - M5
var M4 float64 = M0 + M5

// S-Factors
var S0 float64 = math.Cos(0.0/16.0*math.Pi) / math.Sqrt(8)
var S1 float64 = math.Cos(1.0/16.0*math.Pi) / 2.0
var S2 float64 = math.Cos(2.0/16.0*math.Pi) / 2.0
var S3 float64 = math.Cos(3.0/16.0*math.Pi) / 2.0
var S4 float64 = math.Cos(4.0/16.0*math.Pi) / 2.0
var S5 float64 = math.Cos(5.0/16.0*math.Pi) / 2.0
var S6 float64 = math.Cos(6.0/16.0*math.Pi) / 2.0
var S7 float64 = math.Cos(7.0/16.0*math.Pi) / 2.0

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
	zeroBased                   bool
	componentsInScan            int  // The numnber of components used in the scan
	frameType                   byte // SOF0 or SOF2
	/**/
	blocks          *[]Block
	blockWidth      int
	blockHeight     int
	blockWidthReal  int
	blockHeightReal int
	blockCount      int
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
