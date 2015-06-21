Partion
========

Package Partion is a helper that helps you send partially a request.
put another way, To speed up, you can break down a request to large resource into some request
to small resource that constitutes the large resource to integrate small resource
into large resource you want to get at first.

Example
=======

```go
package main

import (
	"github.com/propag/partion"
	"os"
)

func main() {
	w, _ := os.Create("python-3.4.3.msi")
	tri, _ := partion.New206(
		"https://www.python.org/ftp/python/3.4.3/python-3.4.3.msi",
		w,
		10,
	)

	reactor := partion.NewReactor()
	reactor.SetRequestTripper(tri)
	reactor.SetPracticer(NewPackagePracticer())

	if err := reactor.Wait(); err == nil {
		fmt.Println("Done!")
	} else {
		fmt.Println(err)
	}
}
```
you can download python msi installer by dividing the resource by **10**
