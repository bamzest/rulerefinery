package utils

import (
	"path/filepath"
	"strings"
)

// NormalizeLocalPath 标准化本地文件路径，确保相对路径带 ./ 前缀
// 使用 filepath.Clean 清理路径，然后添加 ./ 前缀
func NormalizeLocalPath(path string) string {
	if path == "" {
		return ""
	}

	// 已经是绝对路径，使用 filepath.Clean 清理后返回
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	// 清理路径（去除冗余的分隔符、. 和 .. 等）
	cleanPath := filepath.Clean(path)

	// 如果清理后是 "."，转换为 "./"
	if cleanPath == "." {
		return "./"
	}

	// 如果已经有 ./ 前缀，保持不变
	if strings.HasPrefix(cleanPath, "."+string(filepath.Separator)) {
		return cleanPath
	}

	// 如果是 ../ 开头，保持不变
	if strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) || cleanPath == ".." {
		return cleanPath
	}

	// 添加 ./ 前缀（手动拼接，避免 filepath.Join 清理掉 .）
	return "." + string(filepath.Separator) + cleanPath
}

// ToRelativePath 将绝对路径转换为相对路径（带 ./ 前缀）
// 使用 filepath.Rel 计算相对路径
func ToRelativePath(workDir, absPath string) string {
	if absPath == "" {
		return ""
	}

	// 如果已经是相对路径，标准化后返回
	if !filepath.IsAbs(absPath) {
		return NormalizeLocalPath(absPath)
	}

	// 使用 filepath.Rel 计算相对路径
	relPath, err := filepath.Rel(workDir, absPath)
	if err != nil {
		// 如果无法计算相对路径（例如跨盘符），返回清理后的绝对路径
		return filepath.Clean(absPath)
	}

	// 标准化相对路径（添加 ./ 前缀）
	return NormalizeLocalPath(relPath)
}

// ToAbsolutePath 将相对路径转换为绝对路径
// 使用 filepath.Join 和 filepath.Abs 处理
func ToAbsolutePath(workDir, relPath string) string {
	if relPath == "" {
		return ""
	}

	// 如果已经是绝对路径，使用 filepath.Clean 清理后返回
	if filepath.IsAbs(relPath) {
		return filepath.Clean(relPath)
	}

	// 使用 filepath.Join 拼接路径，自动处理 ./ 前缀
	absPath := filepath.Join(workDir, relPath)

	// 使用 filepath.Clean 清理路径
	return filepath.Clean(absPath)
}

// IsLocalPath 判断是否为本地文件路径（相对或绝对路径）
// 通过检查是否包含 URL scheme (协议://）来区分本地路径和远程 URL
// 支持识别 http://, https://, ftp://, file:// 等各种 URL 协议
func IsLocalPath(path string) bool {
	if path == "" {
		return false
	}

	// 检查是否包含 URL scheme（协议://）
	// 如果包含 "://" 则认为是 URL，而不是本地路径
	// 这样可以识别 http://, https://, ftp://, ftps://, file://, git:// 等所有 URL 格式
	if strings.Contains(path, "://") {
		return false
	}

	// Windows 盘符路径 (如 C:\path 或 C:/path) 是本地路径
	// 需要排除误判，因为 C: 中的冒号不是 URL scheme
	// filepath.IsAbs 会正确识别 Windows 路径

	return true
}

// CleanLocalPath 清理本地路径，移除 ./ 前缀并规范化
// 使用 filepath.Clean 清理路径
func CleanLocalPath(path string) string {
	if path == "" {
		return ""
	}

	// 使用 filepath.Clean 清理路径
	cleanPath := filepath.Clean(path)

	// 如果结果是 "."，返回空字符串（表示当前目录）
	if cleanPath == "." {
		return ""
	}

	return cleanPath
}

// CompareLocalPaths 比较两个本地路径是否相同
// 使用 filepath.Clean 标准化后比较
func CompareLocalPaths(path1, path2 string) bool {
	if path1 == "" && path2 == "" {
		return true
	}

	if path1 == "" || path2 == "" {
		return false
	}

	// 使用 filepath.Clean 清理后比较
	clean1 := filepath.Clean(path1)
	clean2 := filepath.Clean(path2)

	// 处理 "." 的特殊情况
	if clean1 == "." {
		clean1 = ""
	}
	if clean2 == "." {
		clean2 = ""
	}

	return clean1 == clean2
}
