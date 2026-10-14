# chandler icons

All icons derive from one master image, so every platform asset stays
pixel-consistent. Everything here is committed: builds never need the
generator toolchain.

    generate_icons.py     regenerates the whole set (needs Pillow:
                          python -m pip install pillow; run from repo root)
    chandler-1024.png     master image (rounded-square violet→blue
                          gradient, bold "C" monogram, soft sheen)
    chandler.ico          Windows app icon, 16–256 px
    chandler.icns         macOS bundle icon, 16–512 px
    png/<N>x<N>.png       Linux hicolor set, 16–512 px — packed into the
                          Linux release tarball under
                          icons/hicolor/<size>/apps/chandler.png

## Windows resource files (rsrc_windows_*.syso)

The `rsrc_windows_amd64.syso` / `rsrc_windows_arm64.syso` files at the
repo root are compiled Windows resources (the app icon; no version
manifest). The Go linker picks them up automatically for
`GOOS=windows <GOARCH>` builds — no build-tag or flag needed — which is
why `chandler-windows-*.exe` shows the icon in Explorer and the taskbar.

They are generated once with an external tool and committed, so plain
`go build` needs nothing beyond the toolchain:

    go run github.com/akavel/rsrc@latest -ico icons/chandler.ico -arch amd64 -o rsrc_windows_amd64.syso
    go run github.com/akavel/rsrc@latest -ico icons/chandler.ico -arch arm64 -o rsrc_windows_arm64.syso

Regenerate them only when `chandler.ico` changes; `tools/packager` needs
no changes (it builds whatever `.syso` files are present).
