package gofpdi

/*
 * Copyright (c) 2015 Kurt Jung (Gmail: kurt.w.jung),
 *   Marcus Downing, Jan Slabon (Setasign)
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

import (
	"fmt"
	"os"
	"strings"
	// "regexp"
	"bytes"
	"strconv"
	// "bufio"
	"errors"
	// "github.com/jung-kurt/gofpdf"
	"log"
	"math"
)

const (
	defaultPdfVersion = "1.3"
)

// PDFParser is a high-level parser for PDF elements
// See fpdf_pdf_parser.php
type PDFParser struct {
	reader          *PDFTokenReader // the underlying token reader
	pageNumber      int             // the current page number
	lastUsedPageBox string          // the most recently used page box
	pages           []PDFPage       // already loaded pages

	xref struct {
						maxObject    int                 // the highest xref object number
						xrefLocation int64               // the location of the xref table
						xref         map[ObjectRef]int64 // all the xref offsets
						trailer Dictionary
		 }
	currentObject ObjectDeclaration
	root Dictionary
}

// OpenPDFParser opens an existing PDF file and readies it
func OpenPDFParser(file *os.File) (*PDFParser, error) {
	// fmt.Println("Opening PDF file:", filename)
	reader, err := NewTokenReader(file)
	if err != nil {
		return nil, err
	}

	parser := new(PDFParser)
	parser.reader = reader
	parser.pageNumber = 0
	parser.lastUsedPageBox = DefaultBox

	// read xref data
	offset, err := parser.reader.findXrefTable()
	if err != nil {
		return nil, err
	}

	err = parser.readXrefTable(offset)
	if err != nil {
		return nil, err
	}

	err = parser.readRoot()
	if err != nil {
		return nil, err
	}

	// check for encryption
	if parser.getEncryption() {
		return nil, errors.New("File is encrypted!")
	}

	getPagesObj, err := parser.getPagesObj()
	if err != nil {
		return nil, err
	}

	err = parser.readPages(getPagesObj)
	if err != nil {
		return nil, err
	}

	return parser, nil
}

func (parser *PDFParser) setPageNumber(pageNumber int) {
	parser.pageNumber = pageNumber
}

// Close releases references and closes the file handle of the parser
func (parser *PDFParser) Close() {
	parser.reader.Close()
}

// PDFPage is a page extracted from an existing PDF document
type PDFPage struct {
	Dictionary
	Number int
}

// GetPageBoxes gets the all the bounding boxes for a given page
//
// pageNumber is 1-indexed
// k is a scaling factor from user space units to points
func (parser *PDFParser) GetPageBoxes(pageNumber int, k float64) PageBoxes {
	boxes := make(map[string]*PageBox, 5)
	if pageNumber < 0 || (pageNumber - 1) >= len(parser.pages) {
		return PageBoxes{boxes, DefaultBox}
	}

	page := parser.pages[pageNumber - 1]
	if box := parser.getPageBox(page.Dictionary, MediaBox, k); box != nil {
		boxes[MediaBox] = box
	}
	if box := parser.getPageBox(page.Dictionary, CropBox, k); box != nil {
		boxes[CropBox] = box
	}
	if box := parser.getPageBox(page.Dictionary, BleedBox, k); box != nil {
		boxes[BleedBox] = box
	}
	if box := parser.getPageBox(page.Dictionary, TrimBox, k); box != nil {
		boxes[TrimBox] = box
	}
	if box := parser.getPageBox(page.Dictionary, ArtBox, k); box != nil {
		boxes[ArtBox] = box
	}
	return PageBoxes{boxes, DefaultBox}
}

// getPageBox reads a bounding box from a page.
//
// page is a /Page dictionary.
//
// k is a scaling factor from user space units to points.
func (parser *PDFParser) getPageBox(pageObj Dictionary, boxIndex string, k float64) *PageBox {
	page := pageObj

	var box Value

	// Do we have this box in our page?
	if boxRef, ok := page["/" + boxIndex]; ok {

		// If box is a reference, resolve it.
		if boxRef.Type() == typeObjRef {
			box = parser.resolveObject(boxRef);
			if box == nil {
				return nil
			}
		}
		if boxRef.Type() == typeArray {
			box = boxRef
		}
	}

	if box != nil {
		if box.Type() == typeArray {

			boxDetails := box.(Array)
			log.Println()
			x := boxDetails[0] / k
			y := boxDetails[1] / k
			w := math.Abs(boxDetails[0] - boxDetails[2]) / k
			h := math.Abs(boxDetails[1] - boxDetails[3]) / k
			llx := math.Min(boxDetails[0], boxDetails[2]) / k
			lly := math.Min(boxDetails[1], boxDetails[3]) / k
			urx := math.Max(boxDetails[0], boxDetails[2]) / k
			ury := math.Max(boxDetails[1], boxDetails[3]) / k

			return PageBox{
				gofpdf.PointType{
					x,
					y,
				},
				gofpdf.SizeType{
					w,
					h,
				},
				gofpdf.PointType{
					llx,
					lly,
				},
				gofpdf.PointType{
					urx,
					ury,
				},
			}
		}
	} else {
		// Box not found, take it from the parent.
		if parentPageRef, ok := page["/Parent"]; ok {
			parentPageObj := parser.resolveObject(parentPageRef)
			return parser.getPageBox(parentPageObj.Values[0].(Dictionary), boxIndex, k)
		}
	}

	/*

	   if (!is_null($box) && $box[0] == pdf_parser::TYPE_ARRAY) {
	       $b = $box[1];
	       return array(
	           'x' => $b[0][1] / $k,
	           'y' => $b[1][1] / $k,
	           'w' => abs($b[0][1] - $b[2][1]) / $k,
	           'h' => abs($b[1][1] - $b[3][1]) / $k,
	           'llx' => min($b[0][1], $b[2][1]) / $k,
	           'lly' => min($b[1][1], $b[3][1]) / $k,
	           'urx' => max($b[0][1], $b[2][1]) / $k,
	           'ury' => max($b[1][1], $b[3][1]) / $k,
	       );
	   } else if (!isset($page[1][1]['/Parent'])) {
	       return false;
	   } else {
	       return $this->_getPageBox($this->resolveObject($page[1][1]['/Parent']), $boxIndex, $k);
	   }
	*/

	return nil
}

func (parser *PDFParser) checkXrefTableOffset(offset int64) (int64, error) {
	// if the file is corrupt, it may not line up correctly
	// token := parser.reader.ReadToken()
	// if !bytes.Equal(token, Token("xref")) {
	// 	// bad PDF file! no cookie for you
	// 	// look to see if we can find the xref table nearby
	// 	fmt.Println("Corrupt PDF. Scanning for xref table")
	// 	parser.reader.Seek(-20, 1)
	// 	parser.reader.SkipToToken(Token("xref"))
	// 	token = parser.reader.ReadToken()
	// 	if !bytes.Equal(token, Token("xref")) {
	// 		return errors.New("Corrupt PDF: Could not find xref table")
	// 	}
	// }

	return offset, nil
}

func (parser *PDFParser) readXrefTable(offset int64) error {

	// first read in the Xref table data and the trailer dictionary
	if _, err := parser.reader.Seek(offset, 0); err != nil {
		return err
	}

	lines, ok := parser.reader.ReadLinesToToken(Token("trailer"))
	if !ok {
		return errors.New("Cannot read end of xref table")
	}

	// read the lines, store the xref table data
	start := 1
	if parser.xref.xrefLocation == 0 {
		parser.xref.maxObject = 0
		parser.xref.xrefLocation = offset
		parser.xref.xref = make(map[ObjectRef]int64, len(lines))
	}
	for _, lineBytes := range lines {
		// fmt.Println("Xref table line:", lineBytes)
		line := strings.TrimSpace(string(lineBytes))
		// fmt.Println("Reading xref table line:", line)
		if line != "" {
			if line == "xref" {
				continue
			}
			pieces := strings.Split(line, " ")
			switch len(pieces) {
			case 0:
				continue
			case 2:
				start, _ = strconv.Atoi(pieces[0])
				end, _ := strconv.Atoi(pieces[1])
				if end > parser.xref.maxObject {
					parser.xref.maxObject = end
				}
			case 3:
				// if _, ok := parser.xref.xref[start]; !ok {
				// 	parser.xref.xref[start] = make(map[int]int, len(lines))
				// }
				xr, _ := strconv.ParseInt(pieces[0], 10, 64)
				gen, _ := strconv.Atoi(pieces[1])

				ref := ObjectRef{start, gen}
				if _, ok := parser.xref.xref[ref]; !ok {
					if pieces[2] == "n" {
						parser.xref.xref[ref] = xr
					} else {
						// xref[ref] = nil // ???
					}
				}
				start++
			default:
				return errors.New("Unexpected data in xref table: '" + line + "'")
			}
		}
	}

	// first read in the Xref table data and the trailer dictionary
	if _, err := parser.reader.Seek(offset, 0); err != nil {
		return err
	}

	// Find the trailer token.
	ok = parser.reader.SkipToToken(Token("trailer"))
	if !ok {
		return errors.New("Cannot skip to trailer")
	}

	// Start reading of trailer token.
	parser.reader.ReadToken()

	// Read trailer into dictionary.
	trailer := parser.readValue(nil)
	parser.xref.trailer = trailer.(Dictionary)

	return nil
}

// readRoot reads the object reference for the root.
func (parser *PDFParser) readRoot() (error) {
	if rootRef, ok := parser.xref.trailer["/Root"]; ok {
		if rootRef.Type() != typeObjRef {
			return errors.New("Wrong Type of Root-Element! Must be an indirect reference")
		}

		root := parser.resolveObject(rootRef);
		if root == nil {
			return errors.New("Could not find reference to root")
		}
		parser.root = root.Values[0].(Dictionary)
		return nil
	} else {
		return errors.New("Could not find root in trailer")
	}
}

// getPagesObj gets the pages object from the root element.
func (parser *PDFParser) getPagesObj() (Dictionary, error) {
	if pagesRef, ok := parser.root["/Pages"]; ok {
		if pagesRef.Type() != typeObjRef {
			return nil, errors.New("Wrong Type of Pages-Element! Must be an indirect reference")
		}

		pages := parser.resolveObject(pagesRef);
		if pages == nil {
			return nil, errors.New("Could not find reference to pages")
		}
		return pages.Values[0].(Dictionary), nil
	} else {
		return nil, errors.New("Could not find /Pages in /Root-Dictionary")
	}
}

// readPages parses the PDF Page Object into PDFPages
func (parser *PDFParser) readPages(pages Dictionary) (error) {
	var kids Array
	if kidsRef, ok := pages["/Kids"]; ok {
		if kidsRef.Type() != typeArray {
			return errors.New("Wrong Type of Kids-Element! Must be an array")
		}

		kids = kidsRef.(Array)
		if kids == nil {
			return errors.New("Could not find reference to kids")
		}
	} else {
		return errors.New("Cannot find /Kids in current /Page-Dictionary")
	}

	for k, val := range kids {
		pageObj := parser.resolveObject(val);
		if pageObj == nil {
			return errors.New(fmt.Sprintf("Could not find reference to page %i", k))
		}

		page := PDFPage{
			pageObj.Values[0].(Dictionary),
			(k + 1),
		}
		parser.pages = append(parser.pages, page)
	}

	return nil
}

// getEncryption checks if the pdf has encryption.
func (parser *PDFParser) getEncryption() bool {
	if _, ok := parser.xref.trailer["/Encrypt"]; ok {
		return true
	}
	return false
}

// readValue reads the next value from the PDF
func (parser *PDFParser) readValue(token Token) Value {
	if token == nil {
		token = parser.reader.ReadToken()
	}

	str := token.String()
	switch str {
	case "<":
		// This is a hex value
		// Read the value, then the terminator
		bytes, _ := parser.reader.ReadBytesToToken(Token(">"))
		//fmt.Println("Read hex:", bytes)
		return Hex(bytes)

	case "<<":
		// This is a dictionary.
		// Recurse into this function until we reach
		// the end of the dictionary.
		result := make(map[string]Value, 32)

		validToken := true

		// Skip one line for dictionary.
		for validToken {
			key := parser.reader.ReadToken()
			if (key.Equals(Token(">>"))) {
				validToken = false
				break;
			}

			if key == nil {
				return nil // ?
			}

			value := parser.readValue(nil)
			if value == nil {
				return nil // ?
			}

			// Catch missing value
			if value.Type() == typeToken && value.Equals(Token(">>")) {
				result[key.String()] = Null(struct{}{})
				break
			}

			result[key.String()] = value
		}

		return Dictionary(result)

	case "[":
		// This is an array.
		// Recurse into this function until we reach
		// the end of the array.
		result := make([]Value, 0, 32)
		for {
			// We peek here, as the token could be the value.
			token := parser.reader.ReadToken()
			if token.Equals(Token("]")) {
				break;
			}

			value := parser.readValue(token)
			result = append(result, value)
		}
		return Array(result)

	case "(":
		// This is a string
		openBrackets := 1
		buf := bytes.NewBuffer([]byte{})
		for openBrackets > 0 {
			b, ok := parser.reader.ReadByte()
			if !ok {
				break
			}
			switch b {
			case 0x28: // (
				openBrackets++
			case 0x29: // )
				openBrackets++
			case 0x5C: // \
				b, ok = parser.reader.ReadByte()
				if !ok {
					break
				}
			}
			buf.WriteByte(b)
		}
		return String(buf.Bytes())

	case "stream":
		/*
		// ensure line breaks in front of the stream
		peek := parser.reader.Peek(32)
		for _, c := range peek {
			if !isPdfWhitespace(c) {
				break
			}
			parser.reader.ReadByte()
		}

		// TODO get the stream length
		// lengthObj := parser.currentObject["/Length"]
		// if lengthObj.Type() == typeObjRef {
		// 	lengthObj = lengthObj.(ObjectRef).Resolve()
		// }
		length := 0

		stream, _ := parser.reader.ReadBytes(length)

		if endstream := parser.reader.ReadToken(); endstream.Equals(Token("endstream")) {
			// We don't throw an error here because the next
			// round trip will start at a new offset
		}

		return Stream(stream)
		*/
		return Null(struct{}{})
	}

	if number, err := strconv.Atoi(str); err == nil {
		// A numeric token. Make sure that
		// it is not part of something else.
		if moreTokens := parser.reader.PeekTokens(2); len(moreTokens) == 2 {
			if number2, err := strconv.Atoi(string(moreTokens[0])); err == nil {
				// Two numeric tokens in a row.
				// In this case, we're probably in
				// front of either an object reference
				// or an object specification.
				// Determine the case and return the data
				switch string(moreTokens[1]) {
				case "obj":
					parser.reader.ReadTokens(2)
					return ObjectRef{number, number2}
				case "R":
					parser.reader.ReadTokens(2)
					return ObjectRef{number, number2}
				}
			}
		}

		return Numeric(number)
	}

	if real, err := strconv.ParseFloat(str, 64); err == nil {
		return Real(real)
	}

	if str == "true" {
		return Boolean(true)
	}
	if str == "false" {
		return Boolean(false)
	}
	if str == "null" {
		return Null(struct{}{})
	}
	// Just a token. Return it.
	return token
}

func (parser *PDFParser) resolveObject(spec Value) *ObjectDeclaration {
	// Exit if we get invalid data
	if spec == nil {
		return nil
	}

	if objRef, ok := spec.(ObjectRef); ok {

		// This is a reference, resolve it
		if offset, ok := parser.xref.xref[objRef]; ok {
			originalOffset, _ := parser.reader.Seek(0, 1)
			parser.reader.Seek(offset, 0)
			header := parser.readValue(nil)

			// Check to see if we got the correct object.
			if header != objRef {

				// Reset seeker, we want to find our object.
				parser.reader.Seek(0, 0)
				toSearchFor := Token(fmt.Sprintf("%d %d obj", objRef.Obj, objRef.Gen))
				if parser.reader.SkipToToken(toSearchFor) {
					parser.reader.SkipBytes(len(toSearchFor))
				} else {
					// Unable to find object

					// Reset to the original position
					parser.reader.Seek(originalOffset, 0)
					return nil
				}
			}

			// If we're being asked to store all the information
			// about the object, we add the object ID and generation
			// number for later use
			result := ObjectDeclaration{header.(ObjectRef).Obj, header.(ObjectRef).Gen, make([]Value, 0, 2)}
			parser.currentObject = result

			// Now simply read the object data until
			// we encounter an end-of-object marker
			for {
				value := parser.readValue(nil)
				if value == nil || len(result.Values) > 1 { // ???
					// in this case the parser couldn't find an "endobj" so we break here
					break
				}

				if value.Type() == typeToken && value.Equals(Token("endobj")) {
					break
				}

				result.Values = append(result.Values, value)
			}

			// Reset to the original position
			parser.reader.Seek(originalOffset, 0)

			//log.Print(result)

			return &result

		} else {
			// Unable to find object
			return nil
		}
	}

	if obj, ok := spec.(*ObjectDeclaration); ok {
		return obj
	}
	// Er, it's a what now?
	return nil
}
