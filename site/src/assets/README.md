# Product poster

`workbench.webp` comes from the explicit `hero-poster.png` screenshot in
`zarlcode/docs/images/hero.tape`: the populated **Ready to review** state after a
planned refactor and a passing test in `greeting-demo`. The same recording then
opens the one-file diff (`hero-diff.png`). Do not choose an arbitrary GIF timestamp.

After rendering the canonical hero tape from the repository root:

```sh
ffmpeg -y -i zarlcode/docs/images/hero-poster.png -frames:v 1 -c:v libwebp -update 1 site/src/assets/workbench.webp
```

The homepage initially shows this still. The synchronized GIF only loads after the visitor chooses “Play recording”, and Stop returns to the still. There is no autoplay, including for reduced-motion visitors.
