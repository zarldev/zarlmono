# termimage

Shared, stateless terminal-image primitives extracted from zarlplayer for use by
both zarlplayer and zarlchat. Standard library only; no Bubble Tea dependency,
terminal I/O, caches, background work or environment reads.

- `ID`: deterministic, nonzero ID in Kitty's lower 31-bit namespace.
- `Frame`: cursor-preserving PNG transmission, split into 4096-byte base64 chunks.
- `Placement`: place previously transmitted pixels without uploading again.
- `Delete`: remove one owned image; never emits terminal-global deletion.
- `Cells`: fit pixel dimensions into a cell rectangle (2:1 cell aspect assumption).
- `Fallback`: letterboxed truecolor half-block sampling.

Callers own validated image bytes, geometry limits, output serialization, resize
recovery and cleanup. `Frame` accepts encoded PNG bytes; it is not a decoder or
an untrusted-image validation boundary. The player retains its two-slot cache;
the chat app retains its single selected-image writer. Neither policy belongs
in this stateless package.

```sh
go test -race ./tui/termimage
```
