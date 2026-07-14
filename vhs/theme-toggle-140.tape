Output vhs/out/_throwaway.gif
Set Shell "bash"
Set Theme "Builtin Dark"
Set FontSize 13
Set Width 1240
Set Height 560
Set Padding 6
# Reproduce the bug: dark terminal, but the app started thinking LIGHT (wrong
# auto-detect / a theme change after launch). The glamour light body is near-black
# ink on a dark background — the unreadable state Alex saw.
Env BB_THEME "light"
Type "bash vhs/run-bb.sh"
Enter
Sleep 3s
Type "i"
Sleep 500ms
Type "demo-xy3"
Enter
Sleep 1500ms
Screenshot vhs/out/theme-toggle-before-140.png
Sleep 400ms
# One keypress recovery: T flips the palette + rebuilds the glamour body from the
# explicit dark style — no termenv probe, no hang.
Type "T"
Sleep 1200ms
Screenshot vhs/out/theme-toggle-after-140.png
Sleep 400ms
