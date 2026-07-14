Output vhs/out/_throwaway.gif
Set Shell "bash"
Set FontSize 13
Set Width 1240
Set Height 560
Set Padding 6
Env EMPH_DELAY "4"
Env EMPH_JSON '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"set_view","arguments":{"mode":"tree","title":"the whole tree"}}}'
Type "bash vhs/run-bb-emph.sh"
Enter
Sleep 2500ms
Type "f"
Sleep 2500ms
Screenshot vhs/out/shares-follow-140.png
Sleep 400ms
