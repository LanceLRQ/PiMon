package install

import "encoding/binary"

// 透明光标主题：每个光标名一份相同的 Xcursor 文件，含 24、32、48 三种尺寸，像素全透明。
// 字节布局与 M0 真机验证过（用户实测不可见）的 m0-hidden 主题逐字节一致。

const (
	cursorThemeName = "pimon-hidden"
	cursorThemeFile = "[Icon Theme]\nName=" + cursorThemeName + "\n"
)

// cursorNames 是 M0 主题里的 39 个光标名（含 "resize_"，原样保留）。
var cursorNames = []string{
	"all-scroll", "arrow", "busy", "closedhand", "col-resize", "cross", "crosshair", "default",
	"dnd-move", "e-resize", "ew-resize", "fleur", "forbidden", "grab", "grabbing", "hand",
	"hand1", "hand2", "ibeam", "left_ptr", "move", "nesw-resize", "no-drop", "not-allowed",
	"n-resize", "ns-resize", "nwse-resize", "openhand", "pencil", "plus", "pointer", "progress",
	"question_arrow", "resize_", "row-resize", "s-resize", "text", "wait", "w-resize",
}

var cursorSizes = []uint32{24, 32, 48}

const (
	xcurHeaderLen     = 16
	xcurTocEntryLen   = 12
	xcurImageHdrLen   = 36
	xcurImageChunkTag = 0xfffd0002
)

// xcursorBytes 生成透明 Xcursor 文件内容。
func xcursorBytes() []byte {
	le := binary.LittleEndian
	n := uint32(len(cursorSizes))
	buf := make([]byte, 0, 16000)
	buf = append(buf, "Xcur"...)
	buf = le.AppendUint32(buf, xcurHeaderLen)
	buf = le.AppendUint32(buf, 0x00010000) // 版本字段，字节序为 00 00 01 00
	buf = le.AppendUint32(buf, n)
	pos := uint32(xcurHeaderLen + xcurTocEntryLen*n)
	for _, s := range cursorSizes {
		buf = le.AppendUint32(buf, xcurImageChunkTag)
		buf = le.AppendUint32(buf, s)
		buf = le.AppendUint32(buf, pos)
		pos += xcurImageHdrLen + s*s*4
	}
	for _, s := range cursorSizes {
		for _, v := range []uint32{xcurImageHdrLen, xcurImageChunkTag, s, 1, s, s, 0, 0, 0} {
			buf = le.AppendUint32(buf, v)
		}
		buf = append(buf, make([]byte, s*s*4)...)
	}
	return buf
}
