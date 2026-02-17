```bash
dir2txt complex_test -F 'src/' -f 'src/api/' '!src/api/handler.go'

# 注意顺序：先用 -f 修饰 gitignore，再用 ! 救 log，再用 -F 杀 temp
dir2txt complex_test -f --gitignore '!app.log' -F 'temp/'

dir2txt complex_test -f '*.go' -F '*_test.go' '!main_test.go'

# -f build/ 覆盖默认的 Hard， -F 再次屏蔽 debug.map
dir2txt complex_test -f 'build/' -F 'build/debug.map'

# 测试解包
dir2txt --unwrap test/complex_test_context.md -o test_unwrap/
```