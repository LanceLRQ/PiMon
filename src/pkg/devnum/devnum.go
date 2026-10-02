// Package devnum 解码 stat 得到的设备号，并提供读取文件所在设备与设备节点自身设备号的辅助函数。
package devnum

// Decode 按 glibc 的 major()/minor() 编码拆分设备号。
func Decode(dev uint64) (major, minor uint32) {
	major = uint32((dev>>8)&0xfff | (dev>>32)&^0xfff) //nolint:gosec // 设备号位运算
	minor = uint32((dev & 0xff) | (dev>>12)&^0xff)    //nolint:gosec // 设备号位运算
	return major, minor
}
