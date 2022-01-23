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

func decodeJPEG(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Error! %s\n", err.Error())
		os.Exit(1)
	}
	stat, _ := file.Stat()
	wd, _ := os.Getwd()
	fmt.Printf("Filename: [%s]\n", path.Join(wd, file.Name()))
	fmt.Printf("Filesize: [%d] Bytes\n\n", stat.Size())
	buffer := Buffer{f: file}
	buffer.advance()
	buffer.advance()
	if buffer.bf[1] != 0xFF && buffer.bf[0] != 0xD8 {
		fmt.Printf("Error! The file is not a valid JPEG\n")
		os.Exit(1)
	}
	fmt.Printf("The file is a valid JPEG\n")
	fmt.Printf("******************************\n\n")
	file.Close()
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
