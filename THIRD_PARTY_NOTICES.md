# Third-party components

Drop's Windows version includes these components, each under its own license
(the license files are next to the code in `third_party/`):

| Component | Used for | License file |
| --- | --- | --- |
| [go-webview2](https://github.com/jchv/go-webview2) | the Windows window (Microsoft WebView2) | `third_party/go-webview2/LICENSE` |
| WebView2 loader (Microsoft) | starting WebView2 | `third_party/go-webview2/webviewloader/sdk/LICENSE.txt` |
| [go-winloader](https://github.com/jchv/go-winloader) | loading the WebView2 loader | `third_party/go-winloader/LICENSE.md` |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | Windows system calls | `third_party/golang.org/x/sys/LICENSE` |

Drop is built with the Go standard library, which is licensed under the BSD-3-Clause license.
