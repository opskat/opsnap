// Package web 内嵌管理面板的构建产物
package web

import (
	"embed"
	"io/fs"
)

// dist 是前端构建产物目录。仓库里只提交 dist/.keep，
// 正式构建时由 Makefile 把 frontend/dist 复制到这里再编译。
//
//go:embed all:dist
var dist embed.FS

// placeholder 前端尚未构建时使用的占位页
//
//go:embed placeholder
var placeholder embed.FS

// Dist 返回面板静态文件的根目录：前端已构建时用构建产物，否则用占位页
func Dist() fs.FS {
	return pick(mustSub(dist, "dist"), mustSub(placeholder, "placeholder"))
}

// pick 构建产物里有 index.html 时用构建产物，否则用占位页
func pick(built, fallback fs.FS) fs.FS {
	if _, err := fs.Stat(built, "index.html"); err == nil {
		return built
	}
	return fallback
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		// dir 是编译期确定的合法路径，这里不会出错
		panic(err)
	}
	return sub
}
