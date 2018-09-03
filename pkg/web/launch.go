package web

import (
	"bytes"
	"encoding/json"
	"flag"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/quobyte/k8s-operator/pkg/utils"

	"github.com/quobyte/k8s-operator/pkg/resourcehandler"

	"github.com/golang/glog"
	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write the file to the client.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the client.
	pongWait = 60 * time.Second

	// Send pings to client with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Poll file for changes with this period.
	filePeriod        = 10 * time.Second
	fileName   string = utils.StatusFile
)

var (
	addr      = flag.String("addr", ":8089", "http service address")
	homeTempl = template.Must(template.New("").Parse(homeHTML))
	filename  string
	upgrader  = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
)

func reader(ws *websocket.Conn) {
	defer ws.Close()
	ws.SetReadLimit(512)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			break
		}
	}
}

func writer(ws *websocket.Conn, lastMod time.Time, json bool) {
	lastError := ""
	pingTicker := time.NewTicker(pingPeriod)
	fileTicker := time.NewTicker(filePeriod)
	defer func() {
		pingTicker.Stop()
		fileTicker.Stop()
		ws.Close()
	}()
	for {
		select {
		case <-fileTicker.C:
			var p []byte
			var err error

			p, lastMod, err = readFileIfModified(lastMod)

			if err != nil {
				if s := err.Error(); s != lastError {
					lastError = s
					p = []byte(lastError)
				}
			} else {
				lastError = ""
				if p != nil {
					p = readStatusObject(p, json)
				}
			}
			if p != nil {
				ws.SetWriteDeadline(time.Now().Add(writeWait))
				if err := ws.WriteMessage(websocket.TextMessage, p); err != nil {
					return
				}
			}
		case <-pingTicker.C:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

func serveWs(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if _, ok := err.(websocket.HandshakeError); !ok {
			log.Println(err)
		}
		return
	}

	var lastMod time.Time
	var json bool
	if n, err := strconv.ParseInt(r.FormValue("lastMod"), 16, 64); err == nil {
		lastMod = time.Unix(0, n)
	}

	if json, err = strconv.ParseBool(r.FormValue("json")); err != nil {
		glog.Error(err.Error())
	} else {
		json = json
	}

	go writer(ws, lastMod, json)
	reader(ws)
}

func readFileIfModified(lastModified time.Time) ([]byte, time.Time, error) {
	file, err := os.Stat(fileName)

	if os.IsNotExist(err) {
		return []byte("Client does not exists or Waiting for status updates from server"), lastModified, nil
	}
	if err != nil {
		return nil, lastModified, err
	}
	if !file.ModTime().After(lastModified) {
		return nil, lastModified, nil
	}
	data, err := ioutil.ReadFile(fileName)
	if err != nil {
		return nil, file.ModTime(), err
	}
	return data, file.ModTime(), nil
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	var json bool
	if r.URL.Path == "/json" {
		json = true
	}
	w.Header().Set("Content-Type", "text/html;charset=utf-8")
	data, lastMod, err := readFileIfModified(time.Time{})
	if err != nil {
		data = []byte(err.Error())
		lastMod = time.Unix(0, 0)
	}
	data = readStatusObject(data, json)
	var op = struct {
		Host    string
		Data    string
		LastMod string
		Json    bool
	}{
		r.Host,
		string(data),
		strconv.FormatInt(lastMod.UnixNano(), 16),
		json,
	}
	homeTempl.Execute(w, &op)
}

func readStatusObject(data []byte, jsonVal bool) []byte {
	if !jsonVal {
		var val []resourcehandler.ClientUpdateOnHold
		err := json.Unmarshal(data, &val)
		if err != nil {
			glog.Errorf("error %v", err.Error()) // data in file is not status object
			return data
		}

		var status bytes.Buffer
		tmpl := template.Must(template.ParseFiles("/public/html/status.html"))
		err = tmpl.Execute(&status, val)
		if err != nil {
			glog.Error(err.Error())
		}
		data = status.Bytes()
	}
	return data
}

//StartWebServer Starts webserver on port
func StartWebServer() {
	glog.Infof("Starting server on address: %s", *addr)
	http.HandleFunc("/", statusHandler)
	http.HandleFunc("/json", statusHandler)
	http.HandleFunc("/ws", serveWs)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		glog.Fatal(err)
	}
}

const homeHTML = `<!DOCTYPE html>
<html lang="en">
	<title>Quobyte® - Kubernetes operator</title>
	<link type="image/x-icon" href="data:image/x-icon;base64,
		AAABAAEAEBAAAAEAIABoBAAAFgAAACgAAAAQAAAAIAAAAAEAIAAAAAAAAAQAAAAAAAAAAAAAAAAA
		AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD8mDX9+Sxqffksav35LGr9+SxpIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAfksaUH5LGt9+Sxr/fksa135LGr9+SxqPAAAAAAAAAAB+
		SxqHfksaUAAAAAAAAAAAAAAAAAAAAAAAAAAAfksan35LGv9+SxqfPyYNXwAAAAAAAAAAAAAAAAAAAAB+SxqHfksa/35LGv9+SxqfAAAAAAAAAAAAAAAAfksaWH5LGv9+SxqHAAAAAAAAAAA/Jg04PyYN
		Xz8mDUB+Sxqnfksa/35LGqd+SxrPfksa/35LGlAAAAAAAAAAAH5LGt9+SxqfAAAAAAAAAAB+SxqHfksa/35LGv9+Sxr/fksa/35LGuc/Jg0IAAAAAH5LGu9+Sxr/AAAAAD8mDX9+Sxr/PyYNXwAAAAB+
		SxqHfksa/35LGq8/Jg04PyYNIH5LGr9+Sxr/fksapwAAAAA/Jg0ofksa/z8mDU9+Sxqffksa1wAAAAA/Jg04fksa/35LGo8AAAAAAAAAAAAAAAAAAAAAfksa535LGv8AAAAAAAAAAH5LGvd+Sxq/fksa
		v35LGr8AAAAAPyYNX35LGv8/Jg1vAAAAAAAAAAAAAAAAAAAAAD8mDX9+Sxr/PyYNQAAAAAB+Sxq/fksav35LGs9+Sxq/AAAAAD8mDU9+Sxr/PyYNXwAAAAAAAAAAAAAAAAAAAAA/Jg1/fksa/z8mDUAA
		AAAAfksav35LGr9+Sxq/fksa9wAAAAA/Jg0Qfksa/35LGs8AAAAAAAAAAAAAAAAAAAAAfksap35LGvcAAAAAAAAAAH5LGtd+SxqfPyYNT35LGv8/Jg0oAAAAAH5LGsd+Sxr/fksajz8mDSA/Jg0wfksa
		r35LGv9+SxqHAAAAAD8mDV9+Sxr/PyYNfwAAAAB+Sxr/fksa7wAAAAAAAAAAfksa135LGv9+Sxr/fksa/35LGv9+SxqHAAAAAAAAAAB+Sxqffksa3wAAAAAAAAAAfksaUH5LGv9+SxrPAAAAAAAAAAA/
		Jg0QPyYNVz8mDV8/Jg04AAAAAAAAAAB+SxqHfksa/35LGlgAAAAAAAAAAAAAAAB+Sxqffksa/35LGu8/Jg0oAAAAAAAAAAAAAAAAAAAAAD8mDV9+Sxqffksa/35LGp8AAAAAAAAAAAAAAAAAAAAAAAAA
		AH5LGlh+Sxr/fksa/35LGvd+Sxq/fksav35LGtd+Sxr/fksa335LGlAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD8mDU9+Sxq/fksav35LGr9+SxqfPyYNfwAAAAAAAAAAAAAAAAAAAAAA
		AAAA/H8AAPBvAADHwwAAz4MAAJgZAACxjQAAM8wAADfsAAA37AAAM8wAALGNAACYGQAAz/MAAMfj
		AADwDwAA/D8AAA==" rel="icon" />
    <body>
        <pre id="status">{{.Data}}</pre>
        <script type="text/javascript">
            (function() {
                var data = document.getElementById("status");
                var conn = new WebSocket("ws://{{.Host}}/ws?lastMod={{.LastMod}}&json={{.Json}}");
                conn.onmessage = function(evt) {
                   data.textContent = evt.data;
                }
            })();
        </script>
    </body>
</html>
`
