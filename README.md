# dirgui

@rsnous on [Jan 11, 2021](https://twitter.com/rsnous/status/1348883726642544640): "idea: filesystem<->GUI adapter, where a directory turns into a form, executable files inside that directory turn into buttons in the form, text files into text areas, image files into image views, etc"

@rsnous on [Jan 13, 2021](https://twitter.com/rsnous/status/1349426809088065536): "now wonder if you could make a graphical application that you just VNC into"

And so dirgui was born…

* cmd/dirgui/main.go implements a VNC server to host the GUI, using the RFB 3.3 or 3.8 protocols, specifically
* cmd/dirgui/ui.go implements a GUI (drawn with Go's built-in image library) that creates a widget for each file in a directory, a button for each executable and a single-line text field for all other files

Custom per-file editors are supported. For example, to use a custom editor for foo.gif, build cmd/dirgui-gif and copy/symlink its binary to "foo.gif.gui". dirgui-gif implements a VNC server whose contents will be spliced into dirgui. (Key and pointer events are not yet forwarded, though…)

---

For help, e-mail tom@alltom.com or contact [@alltom](https://twitter.com/alltom) on Twitter

I recommend copying the parts you need into your project. I don't consider this module's API stable at all.
