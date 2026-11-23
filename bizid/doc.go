// Package bizid 按应用时区生成带业务前缀的编号。
// 编号依次拼接原样前缀、12 位年月日时分秒、3 位毫秒及 4 位随机数字。
// 如 PD2610301234560070007 表示前缀 PD、2026 年 10 月 30 日 12:34:56.007 和随机尾号 0007。
// 调用前须初始化 Pixiu 的 infra/env。编号可能重复，要求唯一的存储应使用唯一索引和冲突重试。
//
//	id, err := bizid.Generate("PD")
//	if err != nil {
//		return err
//	}
//	fmt.Println(id)
package bizid
