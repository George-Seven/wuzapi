# whatsmeow patches

This directory stores ordered unified diffs against the exact `whatsmeow` submodule revision recorded by this repository. Apply them in numeric order from the repository root:

```sh
for patch in patches/[0-9]*.patch; do
  git -C whatsmeow apply "../$patch"
done
```

After changing a patch, regenerate it from the `whatsmeow` submodule with:

```sh
git -C whatsmeow diff -- <changed-files> > patches/NNNN-description.patch
```

The submodule revision is pinned separately by the parent repository. Keep patches as ordinary Git diffs; do not commit the patch changes inside the submodule.

Run `./build.sh` for normal builds. It applies unapplied patches in order, skips patches already applied, and stops if a patch matches neither state. It delegates to `build_termux.sh` inside Termux; elsewhere it runs `go build`. The Termux resource extraction smoke test remains available as `./build.sh --run-test` in Termux.
