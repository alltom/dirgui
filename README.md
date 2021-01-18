# dirgui

@rsnous on [Jan 11, 2021](https://twitter.com/rsnous/status/1348883726642544640): "idea: filesystem<->GUI adapter, where a directory turns into a form, executable files inside that directory turn into buttons in the form, text files into text areas, image files into image views, etc"

@rsnous on [Jan 13, 2021](https://twitter.com/rsnous/status/1349426809088065536): "now wonder if you could make a graphical application that you just VNC into"

And so dirgui was born…

* main.go implements a VNC server, the RFB 3.3 protocol specifically
* ui.go implements a GUI that creates a widget for each file in a directory, a button for each executable and a single-line text field for all other files

---

For help, e-mail tom@alltom.com or contact [@alltom](https://twitter.com/alltom) on Twitter
