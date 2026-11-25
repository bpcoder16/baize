// Package share 将宿主的类型化请求编码为分享 token 或完整 URL，并在打开时分发到对应的 Gin handler。
//
// 宿主用 shareboot.Prepare(Product.Register(showProduct)) 装配页面。
// 页面常量与请求结构体可放在业务共用包，生成链接的调用方无需导入 HTTP handler。
//
//	type ProductRequest struct {
//		ID string `json:"id" binding:"required"`
//	}
//	const Product share.Page[ProductRequest] = "product.detail.v1"
//
//	func productURL(ownerID, id string) (string, error) {
//		return Product.BuildURL(ownerID, ProductRequest{ID: id})
//	}
//
//	func productToken(ownerID, id string) (string, error) {
//		return Product.Build(ownerID, ProductRequest{ID: id})
//	}
//
// 仅使用 [Page.Build] 和 [Open] 时可以省略 pageURL。生成完整链接时由宿主从查询串取得唯一的 s 参数，再调用 [Open]。
// 正常轮换时保留原密钥和旧页面解码逻辑，新链接可使用新的密钥或页面版本。
package share
