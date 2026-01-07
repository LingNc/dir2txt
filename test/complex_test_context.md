# Project Structure

```text
complex_test/
├── build
│   ├── app.exe
│   └── debug.map
├── src
│   ├── api
│   │   ├── handler.go
│   │   └── secret.key
│   ├── utils
│   │   └── tools.go
│   └── main_test.go
├── temp
│   ├── cache.tmp
│   └── keep.me
├── .gitignore
├── app.log
└── main.go

```

---

# File Contents

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/.gitignore

```gitignore
*.log
temp/
```

---

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/app.log

```log
Log file content (ignored by git)
```

---

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/main.go

```go
package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}
```

---

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/temp/cache.tmp

```tmp
Temporary cache file
```

---

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/temp/keep.me

```me
This file should be kept
```

---

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/src/main_test.go

```go
package main

import "testing"

func TestMain(t *testing.T) {
	t.Log("Running main test")
}
```

---

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/src/utils/tools.go

```go
package utils

func Add(a int, b int) int {
	return a + b
}
```

---

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/src/api/handler.go

```go
package api

import "fmt"

func HandleRequest() {
	fmt.Println("Handling request")
}
```

---

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/src/api/secret.key

```key
SECRET_KEY=1234567890abcdef
```

---

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/build/app.exe

```exe
Binary content (ignored by git)
```

---

## File: /home/lingnc/workspace/Dir2Txt/test/complex_test/build/debug.map

```map
Debug map content (ignored by git)
```

---

