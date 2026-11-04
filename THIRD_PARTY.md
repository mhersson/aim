# Third-party material

AIM includes material copied from the projects below, each under the MIT License (text at the end) unless its entry names another licence.

## Catppuccin palette

- Source: https://github.com/catppuccin/palette
- Copyright (c) 2021 Catppuccin
- Used in `internal/ui/theme/palette.go`: the colour values of the Latte, Frappé, Macchiato and Mocha flavors.

## Chroma Catppuccin styles

- Source: https://github.com/alecthomas/chroma (`styles/catppuccin-*.xml`, v2.27.0)
- Copyright (C) 2017 Alec Thomas
- Used in `internal/ui/theme/syntax.go`: the mapping from token types to colours and text attributes.

## Gruvbox palette

- Source: https://github.com/morhetz/gruvbox (`colors/gruvbox.vim`)
- By morhetz; the README names the licence, MIT/X11, and no copyright holder
- Used in `themes/gruvbox-*.toml`: the colour values of the dark and light palettes and their hard, medium and soft backgrounds.

## Tokyo Night palette

- Source: https://github.com/folke/tokyonight.nvim (`extras/lua/tokyonight_night.lua`, `_storm.lua`, `_moon.lua` and `_day.lua`)
- By folke, under the Apache License 2.0 (text at the end); the licence names no copyright holder
- Used in `themes/tokyo-night-*.toml`: the colour values of the night, storm, moon and day styles.

## Everforest palette

- Source: https://github.com/sainnhe/everforest (`autoload/everforest.vim`)
- Copyright (c) 2019 sainnhe
- Used in `themes/everforest-*.toml`: the colour values of the dark and light palettes and their hard, medium and soft backgrounds.

## Ayu palette

- Source: https://github.com/ayu-theme/ayu-colors (the `ayu` npm package 9.1.0, `dist/generated/dark.js`, `mirage.js` and `light.js`)
- Copyright (c) Konstantin Pschera <me@kons.ch> (kons.ch)
- Used in `themes/ayu-*.toml`: the colour values of the dark, mirage and light schemes.

## tuios line diff and invisible characters

- Source: https://github.com/Gaurav-Gosain/tuios (`internal/linediff/linediff.go`, `internal/invisible/invisible.go`, `internal/invisible/invisible_test.go`) at commit aaaa128cd35b38ba8aa08a5f69e688f01385ec3e
- Copyright (c) 2025 Gaurav Gosain
- Used in `internal/git/linediff/linediff.go`, `internal/git/invisible/invisible.go` and `internal/git/invisible/invisible_test.go`: the line edit script type, line splitting, the remove-all-then-add-all script and the invisible-character checks.

## go-diff middle snake

- Source: https://github.com/sergi/go-diff (`diffmatchpatch/diff.go`, `diffBisect`) at v1.4.0, a Go port of Google's diff-match-patch (http://code.google.com/p/google-diff-match-patch/)
- Copyright (c) 2012-2016 The go-diff Authors, under the MIT License; the original is Copyright (c) 2006 Google Inc., under the Apache License 2.0 (text at the end)
- Used in `internal/git/linediff/myers.go`: the middle-snake search of `Diff`, adapted to compare lines instead of runes and to give up at a work cap instead of a deadline.

## tuios tiling layouts

- Source: https://github.com/Gaurav-Gosain/tuios (`internal/layout/`, commit `aaaa128cd35b38ba8aa08a5f69e688f01385ec3e`)
- Copyright (c) 2025 Gaurav Gosain
- Used in `internal/core/tiling/`: `arrange.go`, `bsp.go`, `bsp_move.go`, `master.go`, `neighbour.go`, `scrolling.go`, `tiling.go` and their tests (`arrange_test.go`, `bsp_test.go`, `bsp_split_close_test.go`, `bsp_resize_axis_test.go`, `master_test.go`, `neighbour_test.go`, `tiling_test.go`, `export_test.go`).

## tuios diffview

- Source: https://github.com/Gaurav-Gosain/tuios (`internal/diffview/`, commit `aaaa128cd35b38ba8aa08a5f69e688f01385ec3e`)
- Copyright (c) 2025 Gaurav Gosain
- Used in `internal/ui/diffview/`: `diffview.go`, `highlight.go`, `pair.go`, `render.go`, `theme.go` and `lexers/` (`coded.go`, `gen.go`, `registry.go`): the diff line renderer, word-level change ranges and syntax highlighting. `shim.go` is AIM's own.

## chroma lexer definitions

- Source: https://github.com/alecthomas/chroma (`lexers/embedded/*.xml` and the Go and Markdown lexers in `lexers/`) at v2.27.0, as carried by tuios `internal/diffview/lexers/`; the licence is in `internal/ui/diffview/lexers/COPYING`
- Copyright (c) 2017 Alec Thomas
- Used in `internal/ui/diffview/lexers/`: the gzipped `.xml.gz` definitions and `coded.go`.

## fsnotify

- Source: https://github.com/fsnotify/fsnotify at v1.10.1: `fsnotify.go`, `shared.go`, `backend_inotify.go`, `backend_kqueue.go`, `system_bsd.go`, `system_darwin.go`, and from `internal/` the files `internal.go`, `darwin.go`, `freebsd.go`, `debug_darwin.go`, `debug_dragonfly.go`, `debug_freebsd.go`, `debug_kqueue.go`, `debug_linux.go`, `debug_netbsd.go`, `debug_openbsd.go`, `unix.go` and `unix2.go`
- Copyright (c) 2012 The Go Authors and the fsnotify Authors, under the BSD-3-Clause License; the text is in `internal/git/fsnotify/LICENSE`
- Used in `internal/git/fsnotify/`: the file watcher behind `git.Watch`. It differs from upstream in the import path of its `internal` package and in `backend_kqueue.go`, which carries the fix for https://github.com/fsnotify/fsnotify/issues/788 proposed in https://github.com/fsnotify/fsnotify/pull/789, adapted to v1.10.1: one mutex serializes every change to the watch set, `Close` leaves releasing the watches to the event reader and waits for it, and a failed registration closes only a descriptor opened in that call. It is a copy rather than a module requirement so that it can carry a fix upstream has not released yet; a `go.mod` replace would break `go install ...@latest`. The tests there, `close_test.go` and `kqueue_test.go`, are AIM's own.

## Go WebP test image

- Source: https://cs.opensource.google/go/x/image (`testdata/gopher-doc.1bpp.lossless.webp`, v0.47.0)
- Copyright 2011 The Go Authors, under the BSD-3-Clause License (https://github.com/golang/image/blob/master/LICENSE)
- Used in `internal/ui/kitty/testdata/gopher.webp`: the WebP decode test.

## MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

## Apache License 2.0

```text
                                 Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS
```
