package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xproto"

	"github.com/crecheer/wm/keysym"
)

// types
type BitMask uint32

// structs
type Key struct {
	Mod uint16
	Sym uint32
	Fn  func(*xgb.Conn)
}

type Client struct {
	Win      xproto.Window
	X        int16
	Y        int16
	Width    uint16
	Height   uint16
	Floating bool
	Tags     BitMask
}

var keys []Key
var clients = make(map[xproto.Window]*Client)
var focused xproto.Window

// bar
var barWindow xproto.Window
var barGC xproto.Gcontext
var barHeight uint16 = 20 // bar height in pixels, to disable bar set to 0

// layout
var layout string = "tileWithMaster"
var gapSize = 10 // 0 = no border

// tags
var GlobalTags BitMask = 1
var ActiveClients []*Client

func main() {
	conn, err := xgb.NewConn()
	if err != nil {
		log.Fatal(err)
	}

	setup(conn)

	for {
		ev, err := conn.WaitForEvent()
		if err != nil {
			log.Fatal(err)
		}
		//	event loop
		switch e := ev.(type) {
		case xproto.KeyPressEvent:
			handleKeyPress(conn, e)
		case xproto.MapRequestEvent:
			manageWindow(conn, e.Window)
		case xproto.DestroyNotifyEvent:
			unmanageWindow(conn, e.Window)
		//case xproto.UnmapNotifyEvent:
		//	unmanageWindow(conn, e.Window)
		case xproto.EnterNotifyEvent: // change focused window event
			focused = e.Event
			setInputFocus(conn, focused)
			// update bar
			if barHeight != 0 {
				title := getWindowTitle(conn, focused)
				drawBarText(conn, fmt.Sprintf("[%s]", title))
			}
		case xproto.ExposeEvent:
			if e.Window == barWindow {
				title := getWindowTitle(conn, focused)
				if title == "None" {
					title = "wm"
				}
				if barHeight != 0 {
					drawBarText(conn, fmt.Sprintf("[%s]", title))
				}
			}
		default:
			log.Println("event: ", e)
		}

	}
}

func setup(conn *xgb.Conn) {
	root := xproto.Setup(conn).DefaultScreen(conn).Root

	err := xproto.ChangeWindowAttributesChecked(
		conn,
		root,
		xproto.CwEventMask,
		[]uint32{
			xproto.EventMaskSubstructureRedirect |
				xproto.EventMaskSubstructureNotify,
		},
	).Check()

	if err != nil {
		log.Fatal(err)
	}

	setupKeys(conn)
	if barHeight != 0 {
		createBar(conn)
	}
}

func spawn(cmd string, args ...string) {
	c := exec.Command(cmd, args...)
	c.Stdout = nil
	c.Stderr = nil
	c.Stdin = nil
	if err := c.Start(); err != nil {
		log.Println("failed to spawn", cmd, ":", err)
	}
}

func killActiveWindow(conn *xgb.Conn, win xproto.Window) {
	if win == 0 {
		log.Println("no focused window to kill")
		return
	}

	// try to use WM_DELETE_WINDOW to close
	wmProtocols, err := xproto.InternAtom(conn, true,
		uint16(len("WM_PROTOCOLS")), "WM_PROTOCOLS").Reply()
	if err != nil {
		log.Println("failed to get WM_PROTOCOLS atom", err)
		return
	}

	wmDelete, err := xproto.InternAtom(conn, true,
		uint16(len("WM_DELETE_WINDOW")), "WM_DELETE_WINDOW").Reply()
	if err != nil {
		log.Println("failed to get WM_DELETE_WINDOW atom", err)
		return
	}

	data := xproto.ClientMessageDataUnionData32New([]uint32{
		uint32(wmDelete.Atom), uint32(xproto.TimeCurrentTime), 0, 0, 0,
	})

	ev := xproto.ClientMessageEvent{
		Format: 32,
		Window: win,
		Type:   wmProtocols.Atom,
		Data:   data,
	}

	// send event
	err = xproto.SendEventChecked(conn, false, win, xproto.EventMaskNoEvent, string(ev.Bytes())).Check()
	if err != nil {
		// force close if WM_DELETE_WINDOW doesnt work
		log.Println("force closing:", err)
		xproto.DestroyWindow(conn, win)
	}

	delete(clients, win)
	focused = 0
	updateWindows(conn)
}

func setupKeys(conn *xgb.Conn) {
	mod := uint16(xproto.ModMask1)

	// keybinds
	keys = []Key{
		{
			Mod: mod,
			Sym: keysym.XK_Return,
			Fn: func(conn *xgb.Conn) {
				spawn("st")
			},
		},
		{
			Mod: mod | xproto.ModMaskShift,
			Sym: keysym.XK_q,
			Fn: func(conn *xgb.Conn) {
				os.Exit(0)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_q,
			Fn: func(conn *xgb.Conn) {
				killActiveWindow(conn, focused)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_d,
			Fn: func(conn *xgb.Conn) {
				spawn("dmenu_launch")
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_l,
			Fn: func(conn *xgb.Conn) {
				layout = "tileHorizontal"
				updateWindows(conn)
			},
		},
		{
			Mod: mod | xproto.ModMaskShift,
			Sym: keysym.XK_l,
			Fn: func(conn *xgb.Conn) {
				layout = "tileVertical"
				updateWindows(conn)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_m,
			Fn: func(conn *xgb.Conn) {
				layout = "tileWithMaster"
				updateWindows(conn)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_0,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 2 ^ 32 - 1
				switchTags(conn, tags)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_1,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 1
				switchTags(conn, tags)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_2,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 2
				switchTags(conn, tags)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_3,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 4
				switchTags(conn, tags)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_4,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 8
				switchTags(conn, tags)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_5,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 16
				switchTags(conn, tags)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_6,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 32
				switchTags(conn, tags)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_7,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 64
				switchTags(conn, tags)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_8,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 128
				switchTags(conn, tags)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_9,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 256
				switchTags(conn, tags)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_0,
			Fn: func(conn *xgb.Conn) {
				var tags BitMask = 511
				switchTags(conn, tags)
			},
		},
	}
	grabKeys(conn)
}

func grabKeys(conn *xgb.Conn) {
	root := xproto.Setup(conn).DefaultScreen(conn).Root

	for _, k := range keys {
		keycode := keysymToKeycode(conn, k.Sym)
		if keycode == 0 {
			continue
		}

		ignoredMods := []uint16{
			0,
			xproto.ModMaskLock,
			xproto.ModMask2,
			xproto.ModMaskLock | xproto.ModMask2,
		}

		for _, m := range ignoredMods {
			xproto.GrabKey(conn,
				true,
				root,
				k.Mod|m,
				keycode,
				xproto.GrabModeAsync,
				xproto.GrabModeAsync,
			)
		}
	}
}

func keysymToKeycode(conn *xgb.Conn, sym uint32) xproto.Keycode {
	setup := xproto.Setup(conn)
	min := setup.MinKeycode
	max := setup.MaxKeycode

	reply, err := xproto.GetKeyboardMapping(conn, min, byte(max-min+1)).Reply()
	if err != nil {
		return 0
	}

	keysymsPerKeycode := int(reply.KeysymsPerKeycode)

	for kc := min; kc <= max; kc++ {
		for i := 0; i < keysymsPerKeycode; i++ {
			idx := int(kc-min)*keysymsPerKeycode + i
			if uint32(reply.Keysyms[idx]) == sym {
				return kc
			}
		}
	}

	return 0
}

func handleKeyPress(conn *xgb.Conn, e xproto.KeyPressEvent) {
	reply, err := xproto.GetKeyboardMapping(conn, e.Detail, 1).Reply()
	if err != nil || len(reply.Keysyms) == 0 {
		return
	}

	sym := reply.Keysyms[0]

	for _, k := range keys {
		if uint32(sym) != k.Sym {
			continue
		}

		ignoredMods := uint16(xproto.ModMaskLock) | uint16(xproto.ModMask2)
		modifiers := e.State &^ ignoredMods

		if modifiers == k.Mod {
			k.Fn(conn)
		}
	}
}

func setInputFocus(conn *xgb.Conn, win xproto.Window) {
	if win == 0 {
		return
	}
	xproto.SetInputFocus(conn, xproto.InputFocusPointerRoot, win, xproto.TimeCurrentTime)
}

func manageWindow(conn *xgb.Conn, win xproto.Window) {
	// TODO: add window rules
	geom, err := xproto.GetGeometry(conn, xproto.Drawable(win)).Reply()
	if err != nil {
		return
	}

	client := &Client{
		Win:      win,
		X:        geom.X,
		Y:        geom.Y,
		Width:    geom.Width,
		Height:   geom.Height,
		Floating: false,
		Tags:     GlobalTags,
	}

	clients[win] = client
	log.Println("managing window:", win)

	// listen for events on this window
	xproto.ChangeWindowAttributes(conn, win,
		xproto.CwEventMask,
		[]uint32{
			xproto.EventMaskEnterWindow |
				xproto.EventMaskFocusChange |
				xproto.EventMaskPropertyChange,
		})

	// map (show) window
	if (client.Tags & GlobalTags) >= 1 {
		xproto.MapWindow(conn, win)
		focused = win
		setInputFocus(conn, focused)
	}
	// xproto.MapWindow(conn, win)

	// tile all windows
	updateWindows(conn)
}

func unmanageWindow(conn *xgb.Conn, win xproto.Window) {
	if _, ok := clients[win]; ok {
		log.Println("unmanaging window:", win)
		delete(clients, win)
		updateWindows(conn)
	}
}

func switchTags(conn *xgb.Conn, tags BitMask) {
	GlobalTags = tags

	// unmap everything
	for _, client := range clients {
		xproto.UnmapWindow(conn, client.Win)
	}
	// update windows
	updateWindows(conn)

}
func tileVertical(conn *xgb.Conn) {
	screen := xproto.Setup(conn).DefaultScreen(conn)
	screenWidth := int(screen.WidthInPixels)

	usableHeight := int(screen.HeightInPixels) - int(barHeight)

	n := len(ActiveClients)
	if n == 0 {
		return
	}

	heightPerWin := usableHeight / n

	i := 0
	for _, c := range ActiveClients {
		yOffset := int(barHeight) + (i * heightPerWin)
		height := heightPerWin - gapSize
		if i == len(ActiveClients)-1 {
			height = heightPerWin - gapSize*2
		}
		xproto.ConfigureWindow(conn, c.Win,
			xproto.ConfigWindowX|
				xproto.ConfigWindowY|
				xproto.ConfigWindowWidth|
				xproto.ConfigWindowHeight,
			[]uint32{
				0 + uint32(gapSize),       // x
				uint32(yOffset + gapSize), // y
				uint32(screenWidth - gapSize*2),
				uint32(height),
			})

		i++
	}
}

func tileHorizontal(conn *xgb.Conn) {
	screen := xproto.Setup(conn).DefaultScreen(conn)
	screenWidth := int(screen.WidthInPixels)
	screenHeight := int(screen.HeightInPixels)

	n := len(ActiveClients)
	if n == 0 {
		return
	}
	widthPerWin := screenWidth / n
	i := 0
	for _, c := range ActiveClients {
		xOffset := int(i * widthPerWin)
		width := widthPerWin - gapSize
		log.Println(i, len(ActiveClients))
		if i == len(ActiveClients)-1 {
			width = widthPerWin - gapSize*2
		}
		xproto.ConfigureWindow(conn, c.Win,
			xproto.ConfigWindowX|
				xproto.ConfigWindowY|
				xproto.ConfigWindowWidth|
				xproto.ConfigWindowHeight,
			[]uint32{
				uint32(xOffset + gapSize),        // x
				uint32(int(barHeight) + gapSize), // y
				uint32(width),
				uint32(screenHeight - int(barHeight) - gapSize*2),
			})
		i++
	}
}

func tileWithMaster(conn *xgb.Conn) {
	screen := xproto.Setup(conn).DefaultScreen(conn)
	screenWidth := int(screen.WidthInPixels)
	screenHeight := int(screen.HeightInPixels)
	usableHeight := int(screen.HeightInPixels) - int(barHeight)

	var fullscreenMaster int = 0

	n := len(ActiveClients)
	if n == 0 {
		return
	} else if n == 1 {
		fullscreenMaster = 1
	}

	widthPerWin := screenWidth / 2
	masterClient := ActiveClients[0]
	i := 0
	for _, c := range ActiveClients {
		if c == masterClient {
			if fullscreenMaster == 1 {
				xproto.ConfigureWindow(conn, c.Win,
					xproto.ConfigWindowX|
						xproto.ConfigWindowY|
						xproto.ConfigWindowWidth|
						xproto.ConfigWindowHeight,
					[]uint32{
						uint32(0),              // x
						uint32(int(barHeight)), // y
						uint32(screenWidth),    // width
						uint32(screenHeight - int(barHeight)),
					})
			} else {
				xproto.ConfigureWindow(conn, c.Win,
					xproto.ConfigWindowX|
						xproto.ConfigWindowY|
						xproto.ConfigWindowWidth|
						xproto.ConfigWindowHeight,
					[]uint32{
						uint32(0 + gapSize),                  // x
						uint32(int(barHeight) + gapSize),     // y
						uint32(screenWidth/2 - int(gapSize)), // width
						uint32(screenHeight - int(barHeight) - gapSize*2),
					})
			}
		} else {
			heightPerWin := usableHeight / (n - 1)
			yOffset := int(barHeight) + (i * heightPerWin) + gapSize
			xOffset := uint32(widthPerWin + gapSize)
			height := heightPerWin - gapSize*2
			log.Println(len(ActiveClients), i)
			if n > 2 {
				height = heightPerWin - gapSize
			}
			if i >= len(ActiveClients)-2 {
				height = heightPerWin - gapSize*2
			}
			xproto.ConfigureWindow(conn, c.Win,
				xproto.ConfigWindowX|
					xproto.ConfigWindowY|
					xproto.ConfigWindowWidth|
					xproto.ConfigWindowHeight,
				[]uint32{
					xOffset,         // x
					uint32(yOffset), // y
					uint32(widthPerWin - gapSize*2),
					uint32(height),
				})
			i++

		}
	}
}

func createBar(conn *xgb.Conn) {
	screen := xproto.Setup(conn).DefaultScreen(conn)
	root := screen.Root

	// create the window
	cw := []uint32{
		xproto.EventMaskExposure, // redraw on expose
		uint32(0xFFFFFF),         // white background
	}

	barWindow, _ = xproto.NewWindowId(conn)
	xproto.CreateWindow(
		conn,
		screen.RootDepth,
		barWindow,
		root,
		0, 0, // x, y
		screen.WidthInPixels, barHeight,
		0, // border
		xproto.WindowClassInputOutput,
		screen.RootVisual,
		xproto.CwBackPixel|xproto.CwEventMask,
		cw,
	)

	// map bar window
	xproto.MapWindow(conn, barWindow)

	// create bar gc
	barGC, _ = xproto.NewGcontextId(conn)
	xproto.CreateGC(conn, barGC, xproto.Drawable(barWindow), xproto.GcForeground|xproto.GcBackground, []uint32{0x000000, 0xFF0000})
}

func drawBarText(conn *xgb.Conn, text string) {
	if text == "" {
		text = " "
	}

	screen := xproto.Setup(conn).DefaultScreen(conn)
	xproto.ClearArea(conn, false, barWindow, 0, 0, screen.WidthInPixels, barHeight)
	screenWidth := int(screen.WidthInPixels)

	// ascii only because FUCK POLYTEXT8
	ascii := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		if text[i] >= 32 && text[i] <= 126 {
			ascii = append(ascii, text[i])
		}
	}
	if len(ascii) == 0 {
		ascii = []byte(" ")
	}

	textItem := make([]byte, len(ascii)+2)
	textItem[0] = byte(len(ascii)) // string len
	textItem[1] = 0                // offset
	copy(textItem[2:], ascii)

	err := xproto.PolyText8Checked(conn, xproto.Drawable(barWindow), barGC, int16(screenWidth/2-len(ascii)*2), int16(barHeight-5), textItem).Check()
	if err != nil {
		log.Println("polytext failed again :D err:", err)
	}
}

func getWindowTitle(conn *xgb.Conn, win xproto.Window) string {
	if focused == 0 {
		return "None"
	}

	prop, err := xproto.GetProperty(conn, false, focused, xproto.AtomWmName, xproto.AtomString, 0, 100).Reply()

	if err != nil {
		log.Println("failed to get window title:", err)
		return "Unknown"
	}

	if prop == nil || len(prop.Value) == 0 {
		return "Unknown"
	}

	return string(prop.Value)
}

func updateWindows(conn *xgb.Conn) {
	ActiveClients = ActiveClients[:0]
	for _, client := range clients {
		log.Println(client.Tags & GlobalTags)
		if (client.Tags & GlobalTags) >= 1 {
			xproto.MapWindow(conn, client.Win)
			ActiveClients = append(ActiveClients, client)
			log.Println("Mapping window", client.Win)
		}
	}

	switch layout {
	case "tileHorizontal":
		tileHorizontal(conn)
	case "tileVertical":
		tileVertical(conn)
	case "tileWithMaster":
		tileWithMaster(conn)
	default:
		log.Fatal("Unknown layout ", layout)
	}
}
