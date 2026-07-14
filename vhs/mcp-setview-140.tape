Output vhs/out/_throwaway.gif
Set Shell "bash"
Set FontSize 13
Set Width 1240
Set Height 560
Set Padding 6
Env EMPH_DELAY "3.5"
Env EMPH_JSON '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"set_view","arguments":{"mode":"type","query":"status=open","sort_key":"updated","sort_dir":"desc","title":"open, freshest first"}}}'
Type "bash vhs/run-bb-emph.sh"
Enter
Sleep 2s
Type "f"
Sleep 3500ms
Screenshot vhs/out/mcp-setview-140.png
Sleep 400ms
