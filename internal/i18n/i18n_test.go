package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestZhEnKeyParity 把「zh/en 成对维护」这条约定变成机械门：
// 之前它只写在 AGENTS.md 里，靠人记，加一条键漏一条就只在切语言时才暴露
// （T() 的兜底是回退英文，漏译表现为"英文混进中文界面"，不报错）。
func TestZhEnKeyParity(t *testing.T) {
	zh, en := dict[Zh], dict[En]
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("key %q 只在 zh 有，En 缺", k)
		}
	}
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("key %q 只在 en 有，zh 缺", k)
		}
	}
	if len(zh) != len(en) {
		t.Errorf("zh/en 键数不等：%d vs %d", len(zh), len(en))
	}
	for _, lang := range []Lang{Zh, En} {
		for k, v := range dict[lang] {
			if strings.TrimSpace(v) == "" {
				t.Errorf("key %q 在 %s 是空文案", k, lang)
			}
		}
	}
}

var usedKeyRe = regexp.MustCompile(`i18n\.T\(\s*"([a-zA-Z0-9_]+)"`)

// TestEveryUsedKeyExists 扫描全仓 Go 源码里的 i18n.T("…") 引用，逐个确认键存在。
// 漏键的现网表现是 T() 回退到 dict[En][key] 也取不到、返回空串，输出一行空白——
// 门全绿而文案消失，正是最难查的那一类。
func TestEveryUsedKeyExists(t *testing.T) {
	root := filepath.Join("..", "..")
	used := map[string][]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 扫不到的目录不影响本门结论
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "build", "target", ".worktrees":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, m := range usedKeyRe.FindAllStringSubmatch(string(data), -1) {
			used[m[1]] = append(used[m[1]], p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	missing := make([]string, 0)
	for key, sites := range used {
		if _, ok := dict[En][key]; !ok {
			missing = append(missing, key+" ("+strings.Join(sites, ", ")+")")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("代码引用了 %d 个字典里不存在的键：%v", len(missing), missing)
	}
	if len(used) == 0 {
		t.Error("扫描到 0 个 i18n.T 引用——门根本没跑（路径口径变了要同步改这里）")
	}
	t.Logf("引用键 %d 个，全部存在于 zh/en 双语字典；字典共 zh=%d en=%d", len(used), len(dict[Zh]), len(dict[En]))
}
