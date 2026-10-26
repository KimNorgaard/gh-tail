# Records docs/demo.gif and docs/social.png. Run with: just demo

Output docs/demo.gif

Set Shell bash
Set FontSize 15
Set Width 1280
Set Height 640
Set Padding 16
Set Theme "GitHub Dark"
Set TypingSpeed 40ms

# Route "gh tail" to the local build, which may be newer than the installed extension.
Hide
Type `gh() { if [ "$1" = tail ]; then shift; ./gh-tail "$@"; else command gh "$@"; fi; }; clear`
Enter
Show

Type "gh tail -R ghostty-org/ghostty -n 10"
Sleep 500ms
Enter
Sleep 7s
Ctrl+C
Sleep 500ms

Type "gh tail -R ghostty-org/ghostty -n 40 -i"
Sleep 500ms
Enter
Sleep 7s
Up 14
Sleep 1s
Screenshot docs/social.png
Enter
Sleep 4s
Down 8
Sleep 2s
Escape
Sleep 1s
Down 2
Sleep 1s
Type "q"
Sleep 500ms
