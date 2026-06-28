package api

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

type xlsxWorkbook struct {
	Sheets map[string][][]string
}

type xlsxSheetRef struct {
	Name string
	ID   string
}

type xlsxRelationship struct {
	ID     string
	Target string
}

type xlsxCell struct {
	Ref        string
	Type       string
	Value      string
	InlineText string
}

func parseXLSX(data []byte) (xlsxWorkbook, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return xlsxWorkbook{}, fmt.Errorf("无法读取 Excel 文件：%w", err)
	}
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[path.Clean(file.Name)] = file
	}
	sharedStrings, err := readSharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return xlsxWorkbook{}, err
	}
	sheets, err := readWorkbookSheets(files["xl/workbook.xml"])
	if err != nil {
		return xlsxWorkbook{}, err
	}
	relationships, err := readWorkbookRelationships(files["xl/_rels/workbook.xml.rels"])
	if err != nil {
		return xlsxWorkbook{}, err
	}
	result := xlsxWorkbook{Sheets: make(map[string][][]string, len(sheets))}
	for _, sheet := range sheets {
		target, ok := relationships[sheet.ID]
		if !ok {
			return xlsxWorkbook{}, fmt.Errorf("Excel 工作表 %q 缺少文件映射", sheet.Name)
		}
		fileName := path.Clean(path.Join("xl", strings.TrimPrefix(target, "/xl/")))
		if strings.HasPrefix(target, "/") {
			fileName = strings.TrimPrefix(path.Clean(target), "/")
		}
		rows, err := readWorksheet(files[fileName], sharedStrings)
		if err != nil {
			return xlsxWorkbook{}, fmt.Errorf("读取工作表 %q：%w", sheet.Name, err)
		}
		result.Sheets[sheet.Name] = rows
	}
	return result, nil
}

func readSharedStrings(file *zip.File) ([]string, error) {
	if file == nil {
		return nil, nil
	}
	decoder, closeFile, err := zipXMLDecoder(file)
	if err != nil {
		return nil, err
	}
	defer closeFile()
	var values []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 Excel 文本表：%w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "si" {
			continue
		}
		var item struct {
			Texts []string `xml:"t"`
			Runs  []struct {
				Text string `xml:"t"`
			} `xml:"r"`
		}
		if err := decoder.DecodeElement(&item, &start); err != nil {
			return nil, fmt.Errorf("解析 Excel 文本：%w", err)
		}
		value := strings.Join(item.Texts, "")
		for _, run := range item.Runs {
			value += run.Text
		}
		values = append(values, value)
	}
	return values, nil
}

func readWorkbookSheets(file *zip.File) ([]xlsxSheetRef, error) {
	if file == nil {
		return nil, fmt.Errorf("不是有效的 Excel 工作簿：缺少 workbook.xml")
	}
	decoder, closeFile, err := zipXMLDecoder(file)
	if err != nil {
		return nil, err
	}
	defer closeFile()
	var sheets []xlsxSheetRef
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 Excel 工作表列表：%w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sheet" {
			continue
		}
		var ref xlsxSheetRef
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "name":
				ref.Name = attr.Value
			case "id":
				ref.ID = attr.Value
			}
		}
		if ref.Name != "" && ref.ID != "" {
			sheets = append(sheets, ref)
		}
	}
	return sheets, nil
}

func readWorkbookRelationships(file *zip.File) (map[string]string, error) {
	if file == nil {
		return nil, fmt.Errorf("不是有效的 Excel 工作簿：缺少工作表映射")
	}
	decoder, closeFile, err := zipXMLDecoder(file)
	if err != nil {
		return nil, err
	}
	defer closeFile()
	relationships := make(map[string]string)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 Excel 工作表映射：%w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		var relationship xlsxRelationship
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "Id":
				relationship.ID = attr.Value
			case "Target":
				relationship.Target = attr.Value
			}
		}
		if relationship.ID != "" && relationship.Target != "" {
			relationships[relationship.ID] = relationship.Target
		}
	}
	return relationships, nil
}

func readWorksheet(file *zip.File, sharedStrings []string) ([][]string, error) {
	if file == nil {
		return nil, fmt.Errorf("工作表文件不存在")
	}
	decoder, closeFile, err := zipXMLDecoder(file)
	if err != nil {
		return nil, err
	}
	defer closeFile()
	rows := make(map[int]map[int]string)
	maxRow, maxColumn := -1, -1
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("解析工作表：%w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "c" {
			continue
		}
		var cell struct {
			Ref   string `xml:"r,attr"`
			Type  string `xml:"t,attr"`
			Value string `xml:"v"`
			IS    struct {
				Texts []string `xml:"t"`
				Runs  []struct {
					Text string `xml:"t"`
				} `xml:"r"`
			} `xml:"is"`
		}
		if err := decoder.DecodeElement(&cell, &start); err != nil {
			return nil, fmt.Errorf("解析单元格：%w", err)
		}
		rowIndex, columnIndex, err := xlsxCellIndexes(cell.Ref)
		if err != nil {
			continue
		}
		if rowIndex > 5000 || columnIndex > 128 {
			return nil, fmt.Errorf("工作表数据范围过大，请仅保留需要导入的行和列")
		}
		value := cell.Value
		switch cell.Type {
		case "s":
			index, parseErr := strconv.Atoi(strings.TrimSpace(cell.Value))
			if parseErr == nil && index >= 0 && index < len(sharedStrings) {
				value = sharedStrings[index]
			}
		case "inlineStr":
			value = strings.Join(cell.IS.Texts, "")
			for _, run := range cell.IS.Runs {
				value += run.Text
			}
		case "b":
			if cell.Value == "1" {
				value = "TRUE"
			} else {
				value = "FALSE"
			}
		}
		if rows[rowIndex] == nil {
			rows[rowIndex] = make(map[int]string)
		}
		rows[rowIndex][columnIndex] = strings.TrimSpace(value)
		maxRow = max(maxRow, rowIndex)
		maxColumn = max(maxColumn, columnIndex)
	}
	if maxRow < 0 || maxColumn < 0 {
		return [][]string{}, nil
	}
	result := make([][]string, maxRow+1)
	for rowIndex := 0; rowIndex <= maxRow; rowIndex++ {
		result[rowIndex] = make([]string, maxColumn+1)
		for columnIndex, value := range rows[rowIndex] {
			result[rowIndex][columnIndex] = value
		}
	}
	return result, nil
}

func zipXMLDecoder(file *zip.File) (*xml.Decoder, func(), error) {
	if file.UncompressedSize64 > 32<<20 {
		return nil, func() {}, fmt.Errorf("Excel 内容过大")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, func() {}, fmt.Errorf("打开 Excel 内容：%w", err)
	}
	return xml.NewDecoder(reader), func() { _ = reader.Close() }, nil
}

func xlsxCellIndexes(ref string) (int, int, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, 0, fmt.Errorf("单元格地址为空")
	}
	separator := 0
	for separator < len(ref) && ((ref[separator] >= 'A' && ref[separator] <= 'Z') || (ref[separator] >= 'a' && ref[separator] <= 'z')) {
		separator++
	}
	if separator == 0 || separator == len(ref) {
		return 0, 0, fmt.Errorf("无效单元格地址 %q", ref)
	}
	column := 0
	for _, char := range strings.ToUpper(ref[:separator]) {
		column = column*26 + int(char-'A'+1)
	}
	row, err := strconv.Atoi(ref[separator:])
	if err != nil || row < 1 {
		return 0, 0, fmt.Errorf("无效单元格地址 %q", ref)
	}
	return row - 1, column - 1, nil
}

func findHeaderRow(rows [][]string, expected []string) (int, map[string]int, error) {
	for rowIndex, row := range rows {
		if rowIndex > 15 {
			break
		}
		columns := make(map[string]int)
		for columnIndex, value := range row {
			header := normalizeImportHeader(value)
			if header != "" {
				columns[header] = columnIndex
			}
		}
		missing := make([]string, 0)
		for _, header := range expected {
			if _, ok := columns[normalizeImportHeader(header)]; !ok {
				missing = append(missing, header)
			}
		}
		if len(missing) == 0 {
			return rowIndex, columns, nil
		}
	}
	sorted := append([]string(nil), expected...)
	sort.Strings(sorted)
	return 0, nil, fmt.Errorf("找不到模板表头，请确认包含：%s", strings.Join(sorted, "、"))
}

func normalizeImportHeader(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "*")
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func importCell(row []string, columns map[string]int, header string) string {
	index, ok := columns[normalizeImportHeader(header)]
	if !ok || index < 0 || index >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[index])
}

func emptyImportRow(row []string, columns map[string]int, headers []string) bool {
	for _, header := range headers {
		if importCell(row, columns, header) != "" {
			return false
		}
	}
	return true
}
