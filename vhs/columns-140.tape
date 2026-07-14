Output vhs/out/_throwaway.gif
Set Shell "bash"
Set FontSize 13
Set Width 1240
Set Height 560
Set Padding 6
Env EMPH_DELAY "3.5"
Env EMPH_JSON '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"set_view","arguments":{"mode":"columns","root":"demo-rep.1.1","title":"explore demo-rep"}}}'
Type "bash vhs/run-bb-emph.sh"
Enter
Sleep 5s
Screenshot vhs/out/columns-140.png
Sleep 400ms
