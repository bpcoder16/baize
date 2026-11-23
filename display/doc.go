// Package display 提供业务展示使用的时间格式和名称脱敏规则。
// FormatTime 将时间转成应用时区的 RFC3339 字符串，精确到秒。
// nil 返回空字符串，有时间值时须先初始化 Pixiu 的 infra/env。
// MaskName 按 Unicode 码点保留首尾。双字名只保留首字，单字名原样返回。
// 默认脱敏符号为 *，也可指定符号。中间最多重复三个符号。
//
//	fmt.Println(display.FormatTime(nil))         // 空字符串
//	fmt.Println(display.MaskName("张三"))          // 张*
//	fmt.Println(display.MaskName("欧阳娜娜", "#")) // 欧##娜
package display
