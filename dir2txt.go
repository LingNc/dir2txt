package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	gogitignore "github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const (
	version         = "v1.7.4"
	maxDisplayFiles = 24
	keepHeadFiles   = 8
	keepTailFiles   = 8
)

type FilterType int

const (
	TypeKeep FilterType = iota
	TypeSoft
	TypeHard
)

type Rule struct {
	IsGitignore bool
	Pattern     string
	Type        FilterType
}

type Config struct {
	OutputFile   string
	MaxFileSize  int64           // 忽略过大的文件
	TextExts     map[string]bool // 强制视为文本的文件后缀
	NoFold       bool            // 是否关闭目录树文件折叠
	ShowAll      bool            // 是否展示所有文件，忽略内置过滤
	UseGitignore bool            // 是否启用 .gitignore 规则
	Rules        []Rule          // 线性有序规则
}

// walkFollowSymlinks 遍历目录，跟随符号链接的目录，保持逻辑路径用于过滤
func walkFollowSymlinks(root string, fn func(logicalRel string, fullPath string, d os.DirEntry, matcher gogitignore.Matcher) error) error {
	type node struct {
		fsPath   string // 实际文件系统路径（可能为解析后的目标路径）
		rel      string // 相对 root 的逻辑路径（使用符号链接名字串接）
		patterns []gogitignore.Pattern
	}

	stack := []node{{fsPath: root, rel: "", patterns: nil}}
	seen := map[string]bool{}

	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		var matcher gogitignore.Matcher
		var patterns []gogitignore.Pattern
		if config.UseGitignore {
			var err error
			matcher, patterns, err = applyGitignoreFile(n.fsPath, n.rel, n.patterns)
			if err != nil {
				return err
			}
		} else {
			patterns = n.patterns
		}

		entries, err := os.ReadDir(n.fsPath)
		if err != nil {
			return err
		}

		for _, entry := range entries {
			name := entry.Name()
			logicalRel := name
			if n.rel != "" {
				logicalRel = filepath.Join(n.rel, name)
			}

			childFSPath := filepath.Join(n.fsPath, name)
			childIsDir := entry.IsDir()

			// 跟随符号链接目录
			if entry.Type()&os.ModeSymlink != 0 {
				target, err := filepath.EvalSymlinks(childFSPath)
				if err == nil {
					if info, err := os.Stat(target); err == nil && info.IsDir() {
						childIsDir = true
						childFSPath = target
					}
				}
			}

			if err := fn(logicalRel, childFSPath, entry, matcher); err != nil {
				if errors.Is(err, filepath.SkipDir) {
					continue
				}
				return err
			}

			if childIsDir {
				real, err := filepath.EvalSymlinks(childFSPath)
				if err == nil {
					if seen[real] {
						continue
					}
					seen[real] = true
				}
				stack = append(stack, node{fsPath: childFSPath, rel: logicalRel, patterns: patterns})
			}
		}
	}

	return nil
}

func applyGitignoreFile(dirPath string, logicalRel string, parentPatterns []gogitignore.Pattern) (gogitignore.Matcher, []gogitignore.Pattern, error) {
	if !config.UseGitignore {
		return nil, parentPatterns, nil
	}

	patterns := append([]gogitignore.Pattern{}, parentPatterns...)
	giPath := filepath.Join(dirPath, ".gitignore")
	f, err := os.Open(giPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if len(patterns) == 0 {
				return nil, patterns, nil
			}
			return gogitignore.NewMatcher(patterns), patterns, nil
		}
		return nil, nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		base := filepath.ToSlash(logicalRel)
		patterns = append(patterns, gogitignore.ParsePattern(line, splitPath(base)))
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	return gogitignore.NewMatcher(patterns), patterns, nil
}

func splitPath(rel string) []string {
	if rel == "" {
		return []string{}
	}
	return strings.Split(rel, "/")
}

// multiValue 允许通过空格或多次传参传入多个值，例如：
// --filter "*.png *.jpg" --filter "!keep.txt"
type multiValue []string

func (m *multiValue) String() string {
	return strings.Join(*m, ",")
}

func (m *multiValue) Set(s string) error {
	if s == "" {
		return nil
	}
	parts := strings.Fields(s)
	*m = append(*m, parts...)
	return nil
}

// rawStringList 用于存储目录路径，不做空格拆分
// 解决含空格目录名被拆成多个参数的问题
type rawStringList []string

func (m *rawStringList) String() string {
	return strings.Join(*m, ",")
}

func (m *rawStringList) Set(s string) error {
	if s == "" {
		return nil
	}
	*m = append(*m, s)
	return nil
}

// SimpleDirEntry 用于在目录树中创建伪造节点（如省略号）
type SimpleDirEntry struct {
	name  string
	isDir bool
}

func (e *SimpleDirEntry) Name() string               { return e.name }
func (e *SimpleDirEntry) IsDir() bool                { return e.isDir }
func (e *SimpleDirEntry) Type() os.FileMode          { return 0 }
func (e *SimpleDirEntry) Info() (os.FileInfo, error) { return nil, nil }

func parseCommandLine() (rawStringList, string, bool, bool, bool, error) {
	var dirs rawStringList
	var out string
	var help bool
	var install bool
	var uninstall bool

	args := os.Args[1:]
	initRules()

	currentType := TypeHard
	contextSet := false

	appendRule := func(pattern string, ruleType FilterType) {
		if pattern == "" {
			return
		}
		if strings.HasPrefix(pattern, "!") {
			config.Rules = append(config.Rules, Rule{Pattern: strings.TrimPrefix(pattern, "!"), Type: TypeKeep})
			return
		}
		config.Rules = append(config.Rules, Rule{Pattern: pattern, Type: ruleType})
	}

	isPatternArg := func(s string) bool {
		return strings.HasPrefix(s, "!") || strings.ContainsAny(s, "*?[]")
	}

	appendDefaults := func() {
		for _, d := range defaultHardDirs {
			config.Rules = append(config.Rules, Rule{Pattern: d, Type: TypeHard})
		}
		for _, f := range defaultHardFiles {
			config.Rules = append(config.Rules, Rule{Pattern: f, Type: TypeHard})
		}
		for _, ext := range defaultSoftExts {
			config.Rules = append(config.Rules, Rule{Pattern: "*" + ext, Type: TypeSoft})
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--help" || arg == "-h":
			help = true
		case arg == "--install":
			install = true
		case arg == "--uninstall":
			uninstall = true
		case arg == "--all":
			config.ShowAll = true
			config.Rules = nil
		case arg == "--default":
			appendDefaults()
		case arg == "--no-fold":
			config.NoFold = true
		case arg == "--gitignore":
			typ := currentType
			if !contextSet {
				typ = TypeHard
			}
			config.UseGitignore = true
			config.Rules = append(config.Rules, Rule{IsGitignore: true, Type: typ})
		case arg == "--config" || arg == "-c" || arg == "-fc":
			if i+1 >= len(args) {
				return dirs, out, help, install, uninstall, fmt.Errorf("--config 需要一个文件路径")
			}
			i++
			patterns, err := loadPatternsFromFile(args[i])
			if err != nil {
				return dirs, out, help, install, uninstall, err
			}
			typ := TypeSoft
			if arg == "--config" || arg == "-c" {
				if contextSet {
					typ = currentType
				}
			}
			for _, p := range patterns {
				appendRule(p, typ)
			}
		case strings.HasPrefix(arg, "--config="):
			patterns, err := loadPatternsFromFile(strings.TrimPrefix(arg, "--config="))
			if err != nil {
				return dirs, out, help, install, uninstall, err
			}
			typ := TypeSoft
			if contextSet {
				typ = currentType
			}
			for _, p := range patterns {
				appendRule(p, typ)
			}
		case strings.HasPrefix(arg, "-fc="):
			patterns, err := loadPatternsFromFile(strings.TrimPrefix(arg, "-fc="))
			if err != nil {
				return dirs, out, help, install, uninstall, err
			}
			for _, p := range patterns {
				appendRule(p, TypeSoft)
			}
		case arg == "-Fc":
			if i+1 >= len(args) {
				return dirs, out, help, install, uninstall, fmt.Errorf("-Fc 需要一个文件路径")
			}
			i++
			patterns, err := loadPatternsFromFile(args[i])
			if err != nil {
				return dirs, out, help, install, uninstall, err
			}
			for _, p := range patterns {
				appendRule(p, TypeHard)
			}
			currentType = TypeHard
			contextSet = true
		case strings.HasPrefix(arg, "-Fc="):
			patterns, err := loadPatternsFromFile(strings.TrimPrefix(arg, "-Fc="))
			if err != nil {
				return dirs, out, help, install, uninstall, err
			}
			for _, p := range patterns {
				appendRule(p, TypeHard)
			}
			currentType = TypeHard
			contextSet = true
		case arg == "--dir" || arg == "-d":
			consumed := 0
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				dirs.Set(args[i])
				consumed++
			}
			if consumed == 0 {
				return dirs, out, help, install, uninstall, fmt.Errorf("--dir 需要一个路径")
			}
		case strings.HasPrefix(arg, "--dir="):
			dirs.Set(strings.TrimPrefix(arg, "--dir="))
		case arg == "--filter" || arg == "-filter" || arg == "-f":
			currentType = TypeSoft
			contextSet = true
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				appendRule(args[i], TypeSoft)
			}
		case strings.HasPrefix(arg, "--filter="):
			currentType = TypeSoft
			contextSet = true
			appendRule(strings.TrimPrefix(arg, "--filter="), TypeSoft)
		case strings.HasPrefix(arg, "-filter="):
			currentType = TypeSoft
			contextSet = true
			appendRule(strings.TrimPrefix(arg, "-filter="), TypeSoft)
		case arg == "--Filter" || arg == "-Filter" || arg == "-F":
			currentType = TypeHard
			contextSet = true
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				appendRule(args[i], TypeHard)
			}
		case strings.HasPrefix(arg, "--Filter="):
			currentType = TypeHard
			contextSet = true
			appendRule(strings.TrimPrefix(arg, "--Filter="), TypeHard)
		case strings.HasPrefix(arg, "-Filter="):
			currentType = TypeHard
			contextSet = true
			appendRule(strings.TrimPrefix(arg, "-Filter="), TypeHard)
		case arg == "--out" || arg == "-o":
			if i+1 >= len(args) {
				return dirs, out, help, install, uninstall, fmt.Errorf("--out 需要一个路径")
			}
			i++
			out = args[i]
		case strings.HasPrefix(arg, "--out="):
			out = strings.TrimPrefix(arg, "--out=")
		case arg == "--" && i+1 < len(args):
			for _, remain := range args[i+1:] {
				if isPatternArg(remain) {
					typ := currentType
					if !contextSet {
						typ = TypeSoft
					}
					appendRule(remain, typ)
				} else {
					dirs.Set(remain)
				}
			}
			i = len(args)
		default:
			if isPatternArg(arg) {
				typ := currentType
				if !contextSet {
					typ = TypeSoft
				}
				appendRule(arg, typ)
				continue
			}
			dirs.Set(arg)
		}
	}

	if install && uninstall {
		return dirs, out, help, install, uninstall, fmt.Errorf("--install 与 --uninstall 不能同时使用")
	}

	return dirs, out, help, install, uninstall, nil
}

func normalizePattern(pattern string) string {
	if pattern == "" {
		return pattern
	}
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	if strings.HasSuffix(pattern, "/*") {
		base := strings.TrimSuffix(pattern, "/*")
		return base + "/*"
	}
	return strings.TrimSuffix(pattern, "/")
}

func normalizeRules(rules []Rule) []Rule {
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if r.IsGitignore {
			out = append(out, r)
			continue
		}
		r.Pattern = normalizePattern(r.Pattern)
		out = append(out, r)
	}
	return out
}

func loadPatternsFromFile(filePath string) ([]string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("无法读取配置文件 %s: %w", filePath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var patterns []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", filePath, err)
	}
	return patterns, nil
}

// determineOutputPath 计算最终的输出文件路径
func determineOutputPath(dirs []string, userOut string) (string, error) {
	if len(dirs) == 0 {
		return "", fmt.Errorf("至少需要一个目录")
	}
	var absDirs []string
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		absDirs = append(absDirs, filepath.Clean(abs))
	}

	fileName := buildOutputFileName(absDirs)
	if userOut == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(cwd, fileName), nil
	}

	cleanOut := filepath.Clean(userOut)
	dirHint := strings.HasSuffix(userOut, string(os.PathSeparator)) || strings.HasSuffix(userOut, "/") || strings.HasSuffix(userOut, "\\")
	if strings.EqualFold(filepath.Ext(cleanOut), ".md") {
		return cleanOut, nil
	}

	info, err := os.Stat(cleanOut)
	if err == nil && info.IsDir() {
		return filepath.Join(cleanOut, fileName), nil
	}

	// 如果 userOut 以路径分隔符结尾，也当作目录
	if dirHint {
		return filepath.Join(cleanOut, fileName), nil
	}

	// 默认按目录处理，无视是否存在
	return filepath.Join(cleanOut, fileName), nil
}

func buildOutputFileName(absDirs []string) string {
	if len(absDirs) == 1 {
		return fmt.Sprintf("%s_context.md", filepath.Base(absDirs[0]))
	}
	common := findCommonAncestor(absDirs)
	base := "merged_project"
	if common != "" && common != filepath.Dir(common) {
		base = filepath.Base(common)
	}
	if base == "" {
		base = "merged_project"
	}
	return fmt.Sprintf("%s_context.md", base)
}

func findCommonAncestor(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	common := paths[0]
	for _, p := range paths[1:] {
		for !hasPathPrefix(p, common) {
			parent := filepath.Dir(common)
			if parent == common {
				return ""
			}
			common = parent
		}
	}
	return common
}

func hasPathPrefix(pathStr, prefix string) bool {
	rel, err := filepath.Rel(prefix, pathStr)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

var defaultHardDirs = []string{
	".git", ".idea", ".vscode", "node_modules", "__pycache__", "dist", "build", "vendor", "bin", "obj", "target", ".next", "coverage",
}

var defaultHardFiles = []string{
	"dir2txt", "dir2txt.exe", "dir2txt.go",
}

var defaultSoftExts = []string{
	// 图片/媒体
	".png", ".jpg", ".jpeg", ".gif", ".ico", ".svg",
	".mp4", ".mp3", ".wav", ".webp",
	// 压缩包
	".zip", ".tar", ".gz", ".7z", ".rar",
	// 编译产物/二进制
	".exe", ".dll", ".so", ".dylib", ".class", ".pyc", ".o",
	// 字体
	".ttf", ".woff", ".woff2", ".eot",
	// 其他
	".lock", ".pdf", ".ds_store",
}

// 初始化默认配置
var config = Config{
	OutputFile: "project_context.md",
	TextExts: map[string]bool{
		".md": true, ".txt": true, ".log": true,
		".go": true, ".java": true, ".py": true, ".js": true, ".ts": true,
		".c": true, ".cpp": true, ".h": true, ".hpp": true,
		".html": true, ".css": true, ".xml": true, ".yaml": true, ".yml": true,
		".json": true, ".sql": true, ".properties": true, ".ini": true,
		".sh": true, ".bat": true, ".conf": true, ".toml": true,
	},
	MaxFileSize: 1024 * 1024, // 1MB
}

func initRules() {
	config.Rules = nil
	if config.ShowAll {
		return
	}
	for _, d := range defaultHardDirs {
		config.Rules = append(config.Rules, Rule{Pattern: d, Type: TypeHard})
	}
	for _, f := range defaultHardFiles {
		config.Rules = append(config.Rules, Rule{Pattern: f, Type: TypeHard})
	}
	for _, ext := range defaultSoftExts {
		config.Rules = append(config.Rules, Rule{Pattern: "*" + ext, Type: TypeSoft})
	}
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "dir2txt %s\n", version)
		fmt.Fprintf(flag.CommandLine.Output(), "用法: dir2txt [--dir <path> ...] [--filter <pattern> ...] [dir|pattern ...]\n")
		fmt.Fprintf(flag.CommandLine.Output(), "示例:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  dir2txt --dir . ../other --filter '*.png *.jpg' '!keep.png'\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  dir2txt -F build/ -f --gitignore '!build/app.exe'\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  dir2txt . -F src/ -f src/ src/main.go\n")
		fmt.Fprintf(flag.CommandLine.Output(), "参数:\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --dir/-d      指定要扫描的目录，可重复；也可用位置参数追加目录\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --filter/-f   软过滤：跳过内容输出，目录与树仍显示；同时切换后续模式为软\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --Filter/-F   硬过滤：目录树和文件内容都不显示；同时切换后续模式为硬\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --config/-c   指定配置文件路径 (默认按当前上下文，缺省为软)；行首 # 为注释\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  -fc           指定配置文件路径 (始终作为软过滤)；行首 # 为注释\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  -Fc           指定配置文件路径 (始终作为硬过滤)；行首 # 为注释\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --all         清空当前已加载的所有规则 (重置为空)\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --default     在当前规则链位置追加内置默认规则\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --gitignore   将 .gitignore 匹配结果插入规则列表，动作由当前上下文决定 (默认硬)\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --out/-o      指定输出文件路径或输出目录\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --no-fold     在目录树中不折叠长文件列表，始终显示全部文件 (默认超过 %d 个文件折叠)\n", maxDisplayFiles)
		fmt.Fprintf(flag.CommandLine.Output(), "  --install     安装程序到系统 (Linux: /usr/local/bin; Windows: Program Files 并添加 PATH)\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --uninstall   从系统中卸载程序\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  --help/-h     显示此帮助\n")
		// 说明
		fmt.Fprintf(flag.CommandLine.Output(), "说明：\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  Pattern 语法: ? 单字符 (test?.log); * 任意串 (*.go); [] 字符范围 (file[0-9].txt); 前缀 ! 取反 (!important.txt)\n")
		fmt.Fprintf(flag.CommandLine.Output(), "  位置参数      未被 --dir 消耗的参数：若含 * ? [] 或以 ! 开头视为软过滤，其它视为目录\n")
	}

	parsedDirs, outFlag, help, install, uninstall, err := parseCommandLine()
	if help {
		flag.Usage()
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}

	if install {
		if err := manageInstallation(true); err != nil {
			fmt.Fprintf(os.Stderr, "安装失败: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if uninstall {
		if err := manageInstallation(false); err != nil {
			fmt.Fprintf(os.Stderr, "卸载失败: %v\n", err)
			os.Exit(1)
		}
		return
	}

	dirs := []string(parsedDirs)
	config.Rules = normalizeRules(config.Rules)
	if len(dirs) == 0 {
		dirs = append(dirs, ".")
	}

	finalOutPath, err := determineOutputPath(dirs, outFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: 无法确定输出路径: %v\n", err)
		os.Exit(1)
	}

	config.OutputFile = filepath.Base(finalOutPath)
	if err := os.MkdirAll(filepath.Dir(finalOutPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "无法创建输出目录: %v\n", err)
		os.Exit(1)
	}

	outFile, err := os.Create(finalOutPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法创建输出文件: %v\n", err)
		os.Exit(1)
	}
	defer outFile.Close()

	writer := bufio.NewWriter(outFile)
	defer writer.Flush()

	fmt.Printf("结果将写入: %s\n", finalOutPath)

	if err := processDirs(dirs, writer, finalOutPath); err != nil {
		fmt.Fprintf(os.Stderr, "处理目录失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("完成！")
}

type FilterAction int

const (
	ActionKeep FilterAction = iota
	ActionSoftSkip
	ActionHardSkip
)

func evaluatePath(relPath string, isDir bool, rules []Rule, gitMatcher gogitignore.Matcher) FilterAction {
	relSlash := filepath.ToSlash(relPath)
	if relSlash == "." {
		relSlash = ""
	}

	current := ActionKeep

	for _, rule := range rules {
		if rule.IsGitignore {
			if config.UseGitignore && gitMatcher != nil {
				if gitMatcher.Match(splitPath(relSlash), isDir) {
					switch rule.Type {
					case TypeSoft:
						current = ActionSoftSkip
					case TypeHard:
						current = ActionHardSkip
					}
				}
			}
			continue
		}

		if matchPattern(relSlash, isDir, rule.Pattern) {
			switch rule.Type {
			case TypeKeep:
				current = ActionKeep
			case TypeSoft:
				current = ActionSoftSkip
			case TypeHard:
				current = ActionHardSkip
			}
		}
	}

	if current == ActionHardSkip && isDir {
		if isAncestorOfPreservedRule(relSlash, rules) {
			return ActionSoftSkip
		}
	}

	return current
}

// isAncestorOfPreservedRule 检查当前目录是否为某个 Keep/Soft 规则的祖先路径，用于穿透硬过滤目录
func isAncestorOfPreservedRule(dirRel string, rules []Rule) bool {
	dirClean := filepath.ToSlash(dirRel)
	if dirClean == "." || dirClean == "" {
		return true
	}
	dirPrefix := dirClean + "/"

	for _, r := range rules {
		if r.Type == TypeHard {
			continue
		}
		pat := filepath.ToSlash(r.Pattern)
		if pat == dirClean || pat == dirClean+"/" {
			return true
		}
		if strings.HasPrefix(pat, dirPrefix) {
			return true
		}
	}

	return false
}

func processDirs(dirs []string, writer *bufio.Writer, finalOutPath string) error {
	absOut, err := filepath.Abs(finalOutPath)
	if err != nil {
		return err
	}

	writer.WriteString("# Project Structure\n\n")
	writer.WriteString("```text\n")
	for _, dir := range dirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			writer.WriteString(fmt.Sprintf("%s/\n", dir))
			writer.WriteString(fmt.Sprintf("Error generating tree: %v\n", err))
			continue
		}
		writer.WriteString(filepath.Base(absDir) + "/\n")
		if err := writeTree(absDir, absDir, absDir, absDir, "", writer, map[string]bool{}, nil); err != nil {
			writer.WriteString(fmt.Sprintf("Error generating tree for %s: %v\n", dir, err))
		}
		writer.WriteString("\n")
	}
	writer.WriteString("```\n\n")
	writer.WriteString("---\n\n")

	writer.WriteString("# File Contents\n\n")
	var firstErr error
	for _, dir := range dirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "无法获取目录 %s 绝对路径: %v\n", dir, err)
			firstErr = err
			continue
		}
		err = walkFollowSymlinks(absDir, func(logicalRel string, fullPath string, d os.DirEntry, matcher gogitignore.Matcher) error {
			// 排除输出文件自身
			absPath := fullPath
			if absPath == absOut {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			relSlash := filepath.ToSlash(logicalRel)
			if relSlash == "." {
				relSlash = ""
			}

			action := evaluatePath(relSlash, d.IsDir(), config.Rules, matcher)

			switch action {
			case ActionHardSkip:
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			case ActionSoftSkip:
				if d.IsDir() {
					return nil
				}
				display := relSlash
				if display == "" {
					display = filepath.ToSlash(fullPath)
				}
				fmt.Printf("[SKIP] 忽略内容 (Soft Filter): %s\n", display)
				return nil
			case ActionKeep:
				if d.IsDir() {
					return nil
				}
				return processFile(fullPath, writer)
			}

			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "处理目录 %s 时出错: %v\n", dir, err)
			firstErr = err
		}
	}
	return firstErr
}

func manageInstallation(isInstall bool) error {
	if runtime.GOOS == "windows" {
		return manageWindows(isInstall)
	}
	return manageUnix(isInstall)
}

func manageUnix(isInstall bool) error {
	targetDir := "/usr/local/bin"
	targetName := "dir2txt"
	targetPath := filepath.Join(targetDir, targetName)

	if !isInstall {
		fmt.Printf("正在卸载: %s\n", targetPath)
		if err := os.Remove(targetPath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("未找到已安装的程序")
			}
			return fmt.Errorf("卸载失败 (权限不足?): %v", err)
		}
		fmt.Println("[SUCCESS] 卸载成功")
		return nil
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	realPath, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		realPath = exePath
	}

	fmt.Printf("正在安装: %s -> %s\n", realPath, targetPath)

	srcFile, err := os.Open(realPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(targetPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("无法写入目标路径 (请尝试 sudo): %v", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	fmt.Println("[SUCCESS] 安装成功！现在可以在任意位置运行 dir2txt")
	return nil
}

func manageWindows(isInstall bool) error {
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\\Program Files`
	}
	installDir := filepath.Join(programFiles, "dir2txt")
	targetExe := filepath.Join(installDir, "dir2txt.exe")

	if !isInstall {
		fmt.Printf("正在移除文件: %s\n", targetExe)
		os.Remove(targetExe)
		os.Remove(installDir)
		fmt.Println("[SUCCESS] 文件已移除。")
		fmt.Println("[WARNING]  注意: 为了安全起见，程序不会自动修改注册表。请手动从环境变量 PATH 中删除该路径。")
		return nil
	}

	fmt.Printf("正在安装到: %s\n", installDir)
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("无法创建目录 (请以管理员身份运行): %v", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	srcFile, err := os.Open(exePath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(targetExe, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("无法写入文件 (请以管理员身份运行): %v", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	fmt.Println("[SUCCESS] 文件复制成功。")

	fmt.Println("正在配置环境变量...")

	psScript := fmt.Sprintf(`
		$target = "%s"
		$currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
		if ($currentPath -like "*$target*") {
			Write-Host "环境变量已存在，跳过。"
		} else {
			$newPath = $currentPath + ";$target"
			[Environment]::SetEnvironmentVariable("Path", $newPath, "User")
			Write-Host "环境变量已更新。"
		}
	`, installDir)

	cmd := exec.Command("powershell", "-Command", psScript)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("[WARNING] 环境变量自动设置失败: %v\n详情: %s\n请手动将 %s 添加到 PATH\n", err, string(output), installDir)
	} else {
		fmt.Print(string(output))
		fmt.Println("安装完成！请重启终端以生效。")
	}

	return nil
}

// processFile 读取文件并格式化写入 Markdown
func processFile(path string, writer *bufio.Writer) error {
	// 1. 获取文件信息与大小检查
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	// 软链接指向目录时跳过内容读取
	if info.IsDir() {
		fmt.Printf("[SKIP] 软链接指向目录: %s\n", path)
		return nil
	}
	if info.Size() > config.MaxFileSize {
		fmt.Printf("[SKIP] 大文件 (>1MB): %s\n", path)
		return nil
	}

	// 2. 读取文件内容
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(path))
	isForceText := config.TextExts[ext]

	// 3. 二进制检查（非白名单才检查）
	if !isForceText && isBinary(content) {
		fmt.Printf("[SKIP] 检测到二进制文件: %s\n", path)
		return nil
	}

	// 4. 编码检测与转换
	utf8Content, encoding, err := convertToUTF8(content)
	if err != nil {
		fmt.Printf("[WARN] 无法识别文件编码 (已跳过): %s\n", path)
		fmt.Printf("       -> 原因: 内容非 UTF-8 且非 GBK，或包含非法字符。\n")
		return nil
	}

	// 5. 如果发生了转码，发出通知
	if encoding != "UTF-8" {
		fmt.Printf("[INFO] 自动转换编码 [%s -> UTF-8]: %s\n", encoding, path)
	}

	// 6. 写入 Markdown
	fmt.Printf("正在处理: %s\n", path)

	// 标准化路径分隔符
	displayPath := filepath.ToSlash(path)

	// 确定代码块语言标记
	codeBlockLang := strings.TrimPrefix(ext, ".")
	if codeBlockLang == "" {
		codeBlockLang = "text"
	}

	writer.WriteString(fmt.Sprintf("## File: %s\n\n", displayPath))
	writer.WriteString(fmt.Sprintf("```%s\n", codeBlockLang))
	writer.Write(utf8Content)

	// 确保代码块如果没换行符结尾，手动补一个
	if len(utf8Content) > 0 && utf8Content[len(utf8Content)-1] != '\n' {
		writer.WriteString("\n")
	}

	writer.WriteString("```\n\n")
	writer.WriteString("---\n\n")

	return nil
}

// matchPattern 检查单条规则是否匹配路径
// 规则：
// - dir 或 dir/ : 前缀匹配目录及其子项
// - dir/*       : 匹配目录下内容但不匹配目录本身
// - glob        : 使用 path.Match 匹配全路径或文件名
func matchPattern(fullPath string, isDir bool, pattern string) bool {
	if fullPath == "" && pattern == "" {
		return false
	}

	_ = isDir

	full := filepath.ToSlash(fullPath)
	pat := filepath.ToSlash(pattern)

	if strings.HasSuffix(pat, "/*") {
		parent := strings.TrimSuffix(pat, "/*")
		if parent != "" && strings.HasPrefix(full, parent+"/") && full != parent {
			return true
		}
		return false
	}

	pat = strings.TrimSuffix(pat, "/")
	if pat != "" && (full == pat || strings.HasPrefix(full, pat+"/")) {
		return true
	}

	if m, _ := path.Match(pat, full); m {
		return true
	}
	if m, _ := path.Match(pat, filepath.Base(full)); m {
		return true
	}

	return false
}

// isBinary 通过检查内容中是否包含 NUL 字节来简单判断是否为二进制文件
func isBinary(content []byte) bool {
	checkLen := 512
	if len(content) < checkLen {
		checkLen = len(content)
	}

	// 真正的二进制文件通常包含 NUL 字节
	if bytes.IndexByte(content[:checkLen], 0) != -1 {
		return true
	}

	return false
}

// convertToUTF8 尝试将内容转换为 UTF-8
// 返回: (转换后的内容, 原始编码名称, error)
func convertToUTF8(content []byte) ([]byte, string, error) {
	// 1. 先尝试 UTF-8 校验
	if utf8.Valid(content) {
		return content, "UTF-8", nil
	}

	// 2. 尝试 GBK / GB18030 解码
	reader := transform.NewReader(bytes.NewReader(content), simplifiedchinese.GBK.NewDecoder())
	decoded, err := io.ReadAll(reader)
	if err == nil {
		if utf8.Valid(decoded) {
			return decoded, "GBK/GB18030", nil
		}
	}

	// 3. 其他编码可在此扩展
	return nil, "Unknown", fmt.Errorf("encoding not recognized")
}

// writeTree 生成简单的 ASCII 目录树，支持文件折叠，跟随符号链接目录但使用逻辑路径做过滤
func writeTree(rootFS string, rootLogical string, currentFS string, currentLogical string, prefix string, w *bufio.Writer, seen map[string]bool, patterns []gogitignore.Pattern) error {
	var matcher gogitignore.Matcher
	var currentPatterns []gogitignore.Pattern
	if config.UseGitignore {
		var err error
		matcher, currentPatterns, err = applyGitignoreFile(currentFS, currentLogical, patterns)
		if err != nil {
			return err
		}
	} else {
		currentPatterns = patterns
	}

	entries, err := os.ReadDir(currentFS)
	if err != nil {
		return err
	}

	// 过滤掉忽略的项
	var visibleEntries []os.DirEntry
	for _, entry := range entries {
		name := entry.Name()
		logicalPath := filepath.Join(currentLogical, name)
		rel, _ := filepath.Rel(rootLogical, logicalPath)
		relSlash := filepath.ToSlash(rel)

		// 排除输出文件自身
		if name == config.OutputFile {
			continue
		}

		action := evaluatePath(relSlash, entry.IsDir(), config.Rules, matcher)
		if action == ActionHardSkip {
			continue
		}

		visibleEntries = append(visibleEntries, entry)
	}

	// 分离目录与文件，文件过多时折叠
	var dirs []os.DirEntry
	var files []os.DirEntry
	for _, e := range visibleEntries {
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}

	if !config.NoFold && len(files) > maxDisplayFiles {
		display := make([]os.DirEntry, 0, keepHeadFiles+keepTailFiles+1)
		display = append(display, files[:keepHeadFiles]...)
		hiddenCount := len(files) - keepHeadFiles - keepTailFiles
		if hiddenCount < 0 {
			hiddenCount = 0
		}
		display = append(display, &SimpleDirEntry{name: fmt.Sprintf("... (%d files hidden) ...", hiddenCount)})
		display = append(display, files[len(files)-keepTailFiles:]...)
		files = display
	}

	finalEntries := make([]os.DirEntry, 0, len(dirs)+len(files))
	finalEntries = append(finalEntries, dirs...)
	finalEntries = append(finalEntries, files...)

	for i, entry := range finalEntries {
		isLast := i == len(finalEntries)-1

		marker := "├── "
		if isLast {
			marker = "└── "
		}

		displayName := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			fullPath := filepath.Join(currentFS, entry.Name())
			if target, err := os.Readlink(fullPath); err == nil {
				displayName = fmt.Sprintf("%s -> %s", displayName, target)
			}
		}

		w.WriteString(prefix + marker + displayName + "\n")

		childPathFS := filepath.Join(currentFS, entry.Name())
		childPathLogical := filepath.Join(currentLogical, entry.Name())
		childIsDir := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			if target, err := filepath.EvalSymlinks(childPathFS); err == nil {
				if info, err := os.Stat(target); err == nil && info.IsDir() {
					childIsDir = true
					childPathFS = target
				}
			}
		}

		if childIsDir {
			real, err := filepath.EvalSymlinks(childPathFS)
			if err == nil {
				if seen[real] {
					continue
				}
				seen[real] = true
			}
			newPrefix := prefix + "│   "
			if isLast {
				newPrefix = prefix + "    "
			}
			writeTree(rootFS, rootLogical, childPathFS, childPathLogical, newPrefix, w, seen, currentPatterns)
		}
	}
	return nil
}
