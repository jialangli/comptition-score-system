// Package web 内嵌前端静态资源，实现「单个 exe 交付」。
//
// 开发模式（SCORE_DEV=true）下仍从磁盘读 web/index.html，改前端刷新即生效；
// 生产构建从 embed.FS 读，部署时无需额外拷贝静态文件。
package web

import "embed"

//go:embed index.html
var FS embed.FS
