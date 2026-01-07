## Dir to Txt
用于将一个文件夹（仓库）中的所有代码文件扁平化为markdown文本文件，并提供完整的目录结构，便于Ai Coding。

## 构建
```bash
go build dir2txt.go
```

## 更新日志
### v1.0
1. 实现主要完整的功能

### v1.2 - v1.3
1. 增加过滤 --filter, --help 命令行参数。
2. 增加支持选择写入到指定目录 --out, 修复了一些问题。

### v1.4
1. 支持所有参数的简写 -f, -h, -o等。
2. 增加-F, -f 选项，-F表示完全过滤（目录中也没有），-f软过滤（不写入内容但是目录存在）。

### v1.5
1. 支持当目录庞大时会自动折叠，文件数超过 24 时，保留前 8、后 8，中间用占位行 ... (N files hidden) ...。
2. 支持链接文件的读取。
3. 修复所有参数后面跟多个参数项不能识别的问题。
4. 将软过滤和硬过滤逻辑进行调整，修改了一些目录和深层匹配问题，导致和预期行为不一致。
5. 更新帮助信息。

### v1.6
1. 新增支持 --config=路径 形式，支持提供配置文件过滤；-c/--config/-fc 将模式加载到软过滤器中，-Fc 加载到硬过滤器中。
2. 软过滤会被记录操作，显示在控制台。
3. 新增 --no-fold 不折叠目录选项。
4. 修复：传入路径中包含空格的时候会被错误截断。

### v1.7
1. 新增 --install/--uninstall 参数，控制是否安装到系统中并添加环境变量。

#### v1.7.1
1. 修复：-f -F 参数情况下如果有范围过滤选项会覆盖掉后面的 ! 排除项。例如 dir2txt -d xxxx -f '*' '!xxxx.file' 会导致 xxxx.file 一起被排除。
2. 新增：--gitignore 配置项，会按照git的排除方法对该文件夹中包含 .gitignore 进行排除，包括子文件夹的 .gitignore 就像git一样。并且使用该选项的时候会忽略原本自带的 build 等忽略项只会根据 .gitignore 中进行过滤（但是会过滤 .git文件夹）。该选项和 -Fc/-fc 互斥。
3. 新增：--all 配置项，忽略默认排除项，例如 build/、__pycache__ 等，默认将所有文件加入。

#### v1.7.2
1. 修复：-f -F 参数情况下排除项失效情况同v1.7.1，解决上次未完全修复。
2. 新增：--soft/--hard 更高的优先级规定排除的内容是否显示在目录中，软过滤/硬过滤，并且控制参数实际规则。
    1. dir2txt -F 'node_modules' --soft 'node_modules' .
        结果：node_modules 出现在目录树中（--soft 覆盖了 -F）。
    2. dir2txt -f 'src' --hard 'src' .
        结果：src 完全消失（--hard 覆盖了 -f）。
    3. dir2txt --gitignore --soft 'secret.txt' .
        结果：secret.txt 出现在目录树中（--soft 覆盖了 gitignore），但内容不输出。
3. 调整：--gitignore 默认为硬过滤，排除掉的内容不会出现在目录中。

#### v1.7.3
1. 移除：--soft/--hard 控制逻辑，采用更自然的先后顺序优先级方式，默认的排除规则是第一个 -F 的规则，可以通过后面的任何规则去覆盖或者细化调整，-f 和 -F 控制也保持了后面会覆盖前面的方式。
2. 调整：--gitignore 要改变其过滤性只需要在前面的加上 -f 即可（默认硬过滤），并且也会应用默认过滤规则，如果需要全部展开请在最开始使用 --all。
3. 修复：在同一个过滤器中 -f 'build' '!build/*.exe' 类似这样的规则后面不能正确覆盖前面的规则。
4. 提示：对于任何一个选项在哪里用就哪里具有对应的优先级，比如 --all 应该最开始用，如果最后用会清除前面所有的规则。
例如：
1. dir2txt 无参数 默认相当于 `dir2txt -F '.git' '.idea' '.vscode' 'node_modules' '__pycache__' 'dist' 'build' 'vendor' 'bin' 'obj' 'target' '.next' 'coverage' -f '*.png' '*.jpg' '*.jpeg' '*.gif' '*.ico' '*.svg' '*.mp4' '*.mp3' '*.wav' '*.webp' '*.zip' '*.tar' '*.gz' '*.7z' '*.rar' '*.exe' '*.dll' '*.so' '*.dylib' '*.class' '*.pyc' '*.o' '*.ttf' '*.woff' '*.woff2' '*.eot' '*.lock' '*.pdf' '*.ds_store'`
2. dir2txt -f node_modules 可以将 node_modules 目录保留在目录树中，但是不输出内容。
3. dir2txt -f --gitignore 可以根据 .gitignore 规则进行软过滤。
4. dir2txt --all --gitignore 可以先清除所有规则之后再应用规则。
5. dir2txt -F src/ -f src/main.go 可以将 src 目录完全过滤掉，但是保留 main.go 文件。

#### v1.7.4
1. 新增 --default 命令用来添加默认规排除规则。
2. 调整 --all 行为为清除之前的所有规则。

### v1.8.0
1. 新增 --unwrap 输入由 dir2txt 打包的单文件内容还原回文件夹结构。