Output vhs/out/_throwaway.gif
Set Shell "bash"
Set FontSize 13
Set Width 1240
Set Height 560
Set Padding 6
Env EMPH_DELAY "5"
Env EMPH_JSON '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"emphasize","arguments":{"style":"outline","label":"root cause is here","targets":[{"kind":"section","ref":"description"}]}}}'
Type "bash vhs/run-bb-emph.sh"
Enter
Sleep 1500ms
Type "f"
Sleep 500ms
Type "i"
Sleep 400ms
Type "demo-xy3"
Enter
Sleep 3500ms
Screenshot vhs/out/emphasis-section-140.png
Sleep 400ms
