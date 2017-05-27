// program to match persons with group preferences to their groups
package main

//TODO: localize font

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"time"

	"sort"

	"github.com/asticode/go-astilectron"
	"github.com/elazarl/go-bindata-assetfs"
	"github.com/veecue/GroupMatcher/matching"
	"github.com/veecue/GroupMatcher/parseInput"
)

type Message struct {
	Cmd  string
	Body string
}

//go:generate go-bindata static locales templates

// map of all supported languages
var langs map[string]map[string]string

// current language
var l map[string]string

// path to safe the current project to on exit
var autosafepath = path.Join(os.TempDir(), "gm_autosave.json")

// current project
var persons []*matching.Person
var groups []*matching.Group
var filename string

// buffer to save messages to be sent to astilectron
var messages []Message

// last path the project was saved to
var projectPath string

var w *astilectron.Window

// scan language files from the locales directory and import them into the program
func initLangs() {
	langFiles, err := AssetDir("locales")
	if err != nil {
		log.Fatal(err)
	}
	langs = make(map[string]map[string]string, len(langFiles))
	for _, filename := range langFiles {
		if strings.HasSuffix(filename, ".json") {
			langname := strings.TrimSuffix(filename, ".json")
			lang := make(map[string]string)
			langData, err := Asset("locales/" + filename)
			if err != nil {
				log.Fatal(err)
			}
			err = json.Unmarshal(langData, &lang)
			if err != nil {
				log.Fatal(err)
			}
			langs[langname] = lang
		}
	}
}

func init() {
	rand.Seed(int64(time.Now().Nanosecond()))
}

// create a user-readable and localized error message for input parsing
func separateError(err string) (text string, with bool, line int) {
	b := []byte(err)
	for i := range b {
		n, err := strconv.Atoi(string(b[i]))
		if err != nil {
			text = text + string(b[i])
		} else {
			with = true
			line = line*10 + n
		}
	}
	return
}

// store current project to autosafe location on program exit
func autosafe() {
	j, err := matching.ToJSON(groups, persons)
	if err != nil {
		log.Fatal(err)
	}

	ioutil.WriteFile(autosafepath, j, 0600)
}

// autosafe and exit program
func exit() {
	autosafe()
	os.Exit(0)
}

// read and import a possibly produced autosafe
func restoreFromAutosave() {
	j, err := ioutil.ReadFile(autosafepath)
	if err != nil {
		return
	}
	groups, persons, err = matching.FromJSON(j)
	// ignore errors
}

// sorte the persons by alphabet for better UI
func sortPersons() {
	matching.Sort(persons)
	for _, g := range groups {
		matching.Sort(g.Members)
	}
}

// http handler function for the main GUI
func handleRoot(res http.ResponseWriter, req *http.Request) {
	tData, err := Asset("templates/workspace.tmpl")
	if err != nil {
		log.Fatal(err)
	}
	t, err := template.New("workspace").Parse(string(tData))
	if err != nil {
		log.Fatal(err)
	}

	body := handleChanges(req.URL.Query(), req.PostFormValue("data"))

	type Data struct {
		Body template.HTML
	}

	var bodyHTML template.HTML

	bodyHTML = template.HTML(body)

	t.Execute(res, Data{bodyHTML})
}

//handle changes
func handleChanges(form url.Values, data string) string {
	res := bytes.Buffer{}

	var errors bytes.Buffer
	var notifications bytes.Buffer
	var err error

	// handle internal links
	if form["internalLink"] != nil {
		messages = append(messages, Message{"internalLink", form["internalLink"][0]})
	}

	// remove all persons from their groups
	if form["reset"] != nil {
		for i := range groups {
			groups[i].Members = make([]*matching.Person, 0)
		}
		notifications.WriteString(l["restored"] + "<br>")
	}

	var importError string
	var errorLine int
	errorLine = 0
	if form["import"] != nil {
		p := form.Get("import")
		if p != "undefined" { // user pressed cancel, do nothing
			err := handleImport(p)
			// display any error messages from import
			if err == nil {
				projectPath = p
				importError = "success"
			} else {
				importError = err.Error()
			}
		}
	}

	if form["save_as"] != nil {
		p := form.Get("save_as")
		if p != "undefined" { // user pressed cancel, do nothing
			err := handleSaveAs(p)
			// display any error messages from export
			if err == nil {
				projectPath = p
				notifications.WriteString(l["save_success"])
			} else {
				errors.WriteString(l["save_error"])
			}
		}
	}

	if form["export_limited"] != nil {
		p := form.Get("export_limited")
		if p != "undefined" { // user pressed cancel, do nothing
			err := handleExport(p, false)
			// display any error messages from export
			if err == nil {
				notifications.WriteString(l["save_success"])
			} else {
				errors.WriteString(l["save_error"])
			}
		}
	}

	if form["export_total"] != nil {
		p := form.Get("export_total")
		if p != "undefined" { // user pressed cancel, do nothing
			err := handleExport(p, true)
			// display any error messages from export
			if err == nil {
				notifications.WriteString(l["save_success"])
			} else {
				errors.WriteString(l["save_error"])
			}
		}
	}

	// generate new project based on randomness(not a gui feature/only available through url)
	if form["generate"] != nil {
		groups, persons = genGroupsAndPersons()
		notifications.WriteString(l["generated"] + "<br>")
	}

	// find all selected persons that should be affected by any action
	listedPersons := make([]*matching.Person, 0)
	for formName := range form {
		if strings.HasPrefix(formName, "person") {
			i, err := strconv.Atoi(strings.TrimPrefix(formName, "person"))
			if err != nil {
				log.Fatal(err)
			}
			listedPersons = append(listedPersons, persons[i])
		}
	}

	// match selected persons if requested
	if form["match"] != nil {
		var qPersons []*matching.Person

		if len(listedPersons) > 0 {
			qPersons = listedPersons
		} else {
			qPersons = persons
		}
		m := matching.NewMatcher(matching.GetGrouplessPersons(qPersons, groups), groups)
		err, errGroups := m.CheckMatcher()
		if err == nil || err.Error() == "group_deleted" {
			if err != nil {
				errors.WriteString(l["group_deleted"] + errGroups + "<br>")
			}
			err = m.MatchManyAndTakeBest(50, time.Minute, 10*time.Second)
			if err != nil {
				errors.WriteString(l[err.Error()] + "<br>")
			}
			groups, persons = m.Groups, m.Persons
		} else {
			if err.Error() == "combination_overfilled" {
				errors.WriteString(l["combination_overfilled"] + errGroups + "<br>")
			} else {
				errors.WriteString(l[err.Error()] + "<br>")
			}
		}
	}

	// delete the selected persons from the given groups
	if form["delfrom"] != nil {
		j, err := strconv.Atoi(form.Get("delfrom"))
		if err != nil {
			log.Fatal(err)
		}
		for _, p := range listedPersons {
			i := p.IndexIn(groups[j].Members)
			if i != -1 {
				groups[j].Members = groups[j].Members[:i+copy(groups[j].Members[i:], groups[j].Members[i+1:])]
			}
		}
	}

	// add the selected persons to a given groups
	if form["addto"] != nil {
		j, err := strconv.Atoi(form.Get("addto"))
		if err != nil {
			log.Fatal(err)
		}
		for _, p := range listedPersons {
			var hasThisPreference bool
			for k := range p.Preferences {
				if p.Preferences[k] == groups[j] {
					hasThisPreference = true
				}
			}
			//check if person has the wanted preference and is not assigned in case someone messes around with the links (DAU-safety) safety
			if hasThisPreference && matching.FindPerson(p.Name, matching.GetGrouplessPersons(persons, groups)) != nil {
				groups[j].Members = append(groups[j].Members, p)
				hasThisPreference = false
			}
		}
	}

	// handle editmode and import from editmode
	editmode := false
	editmodeContent := ""
	if form["edit"] != nil {
		if data != "" {
			groups, persons, err = parseInput.ParseGroupsAndPersons(strings.NewReader(data))
			if err != nil {
				importError = err.Error()
				editmodeContent = data
				editmode = true
			} else {
				importError = "success"
			}
		} else {
			editmode = true
			editmodeContent, err = parseInput.FormatGroupsAndPersons(groups, persons)
			if err != nil {
				errors.WriteString(l[err.Error()] + "<br>")
			}
		}
	}

	if importError == "success" {
		notifications.WriteString(l["import_success"] + "<br>")
	} else if importError != "" {
		text, withLine, line := separateError(importError)
		errString := l["import_error"] + l[text]
		if withLine {
			errorLine = line
			errString = errString + l["line"] + strconv.Itoa(line)
		}
		groups = make([]*matching.Group, 0)
		persons = make([]*matching.Person, 0)
		errors.WriteString(errString)
	}

	// clear if in invalid state or requested
	if ((groups == nil || persons == nil) && errors.Len() == 0) || form["clear"] != nil {
		groups = make([]*matching.Group, 0)
		persons = make([]*matching.Person, 0)
		notifications.WriteString(l["cleared"] + "<br>")
	}

	var aboutmode bool
	if form["about"] != nil {
		aboutmode = true
	}

	if aboutmode {
		//TODO: add proper html here
		return "about"
	}

	// calculate matching quote for display
	quote_value, quoteInPercent := matching.NewMatcher(persons, groups).CalcQuote()

	// sort persons before display
	sortPersons()

	// create menu:
	if editmode {
		res.WriteString(`<div class="header"><ul><li><a onclick="astilectron.send('/')">` + l["return"] + `</a></li></ul></li></div>`)
	} else {
		res.WriteString(`<div class="header"><ul><li><a onclick="astilectron.send('/?clear')">` + l["reset"] + `</a></li><li><a onclick="astilectron.send('/?reset')">` + l["restore"] + `</a></li><li><a onclick="astilectron.send('/?match')">` + l["match_selected"] + `</a></li></ul></li></div>`)
	}

	// sidebar

	res.WriteString(`<div id="scale_container"><div id="scale" style="height: ` + strconv.FormatFloat(quoteInPercent, 'f', 2, 64) + `vh;">` + strconv.FormatFloat(quote_value, 'f', 2, 64) + `</div></div>`)

	res.WriteString(`<div class="sidebar">`)

	for i, group := range groups {
		htmlid := fmt.Sprint("g", i)
		if (len(group.Members) < group.MinSize || len(group.Members) > group.Capacity) && !matching.AllEmpty(groups) {
			res.WriteString(`<a class="unfitting group" href="#` + htmlid + `" style="background: linear-gradient(90deg, blue ` + strconv.FormatFloat(float64(len(group.Members)*100)/float64(group.Capacity), 'f', 2, 64) + `%, gray ` + strconv.FormatFloat(float64(len(group.Members)*100)/float64(group.Capacity), 'f', 2, 64) + `%);">` + group.Name + `</a>`)
		} else {
			res.WriteString(`<a href="#` + htmlid + `" style="background: linear-gradient(90deg, blue ` + strconv.FormatFloat(float64(len(group.Members)*100)/float64(group.Capacity), 'f', 2, 64) + `%, gray ` + strconv.FormatFloat(float64(len(group.Members)*100)/float64(group.Capacity), 'f', 2, 64) + `%);" class="group">` + group.Name + `</a>`)
		}
	}

	fmt.Fprintf(&res, `</div><div id="controls"><ul><li><a onclick="astilectron.send('?edit')">%s</a></li>`, l["edit"])

	res.WriteString(`</ul></div>`)

	res.WriteString(`<div id="content">`)
	// print notifications and errors:
	if errors.Len() > 0 {
		res.WriteString(`<div class="errors">` + errors.String() + `</div>`)
	}
	if notifications.Len() > 0 {
		res.WriteString(`<div class="notifications">` + notifications.String() + `</div>`)
	}

	res.WriteString(`<div id="panels">`)
	// list unassigned persons:
	if !editmode {

		grouplessPersons := matching.GetGrouplessPersons(persons, groups)
		if !editmode && len(grouplessPersons) > 0 {
			res.WriteString(`<table class="left panel"><form action="">`)
			res.WriteString(`<tr class="heading-big unassigned"><td colspan="5"><h3>` + l["unassigned"] + `</h3></td></tr>`)
			res.WriteString(`<tr class="headings-middle unassigned"><th><span class="spacer"></span></th><th>` + l["name"] + `</th><th>` + l["1stchoice"] + `</th><th>` + l["2ndchoice"] + `</th><th>` + l["3rdchoice"] + `</th></tr>`)
			//res.WriteString( `<tr><td colspan="5"><button type="submit" name="match">` +  + `</button></td></tr>`, l["match_selected"])
			for i, person := range grouplessPersons {
				res.WriteString(`<tr class="person unassigned"><td><!--input type="checkbox" name="person` + strconv.Itoa(i) + `"--></td><td>` + person.Name + `</td>`)

				for i := 0; i < 3; i++ {
					if i >= len(person.Preferences) {
						res.WriteString(`<td>--------</td>`)
					} else {
						prefID := person.Preferences[i].IndexIn(groups)
						res.WriteString(`<td><a onclick="astilectron.send('?person` + strconv.Itoa(person.IndexIn(persons)) + `&addto=` + strconv.Itoa(prefID) + `')" title="` + l["add_to_group"] + `">` + person.Preferences[i].StringWithSize() + `</a></td>`)
					}
				}

				res.WriteString("</tr>")
			}
			res.Write([]byte(`</form></table>`))
		}

		// list group and their members
		if !editmode && len(groups) > 0 {
			res.WriteString("<table class=\"right panel\">")
			for i, group := range groups {
				htmlid := fmt.Sprint("g", i)
				res.Write([]byte(`<form action="">`))
				res.WriteString(`<tr class="heading-big assigned"><td colspan="5"><h3 id="` + htmlid + `">` + group.StringWithSize() + `</h3></td></tr>`)
				res.WriteString(`<tr class="headings-middle assigned"><th><span class="spacer"></span></th><th>` + l["name"] + `</th><th>` + l["1stchoice"] + `</th><th>` + l["2ndchoice"] + `</th><th>` + l["3rdchoice"] + `</th></tr>`)
				for _, person := range group.Members {
					res.WriteString(`<tr class="person assigned"><td><!--input type="checkbox" name="person` + strconv.Itoa(i) + `"--></td><td>` + person.Name + `</td>`)

					for j := 0; j < 3; j++ {
						if j >= len(person.Preferences) {
							res.WriteString(`<td>--------</td>`)
						} else {
							pref := person.Preferences[j]
							prefID := pref.IndexIn(groups)
							personID := person.IndexIn(persons)
							if pref == group {
								res.WriteString(`<td><a onclick="astilectron.send('/?person` + strconv.Itoa(personID) + `&delfrom=` + strconv.Itoa(i) + `&internalLink=#` + htmlid + `')" class="blue" title="` + l["rem_from_group"] + `">` + pref.StringWithSize() + `</a></td>`)
							} else {
								res.WriteString(`<td><a onclick="astilectron.send('/?person` + strconv.Itoa(personID) + `&delfrom=` + strconv.Itoa(i) + `&addto=` + strconv.Itoa(prefID) + `&internalLink=#` + htmlid + `')" title="` + l["add_to_group"] + `">` + pref.StringWithSize() + `</a></td>`)
							}
						}
					}

					res.WriteString("</tr>")
				}
				res.Write([]byte(`</form>`))
			}
			res.WriteString("</table>")
		}
	}
	// display editmode panel with texbox
	if editmode {
		res.WriteString(`<div class="panel"><form action="/?edit" method="POST">`)
		res.WriteString(`<textarea id="edit" name="data" line="` + strconv.Itoa(errorLine) + `">` + editmodeContent + `</textarea>`)
		res.WriteString(`<button type="submit">` + l["save"] + `</button>`)
		res.WriteString(`</form></div>`)
	}

	// end document
	res.WriteString("</div>")
	res.WriteString(`</div>`)

	return res.String()
}

// handle file-uploads for import
func handleImport(filepath string) (err error) {
	file, err := os.Open(filepath)
	if err != nil {
		return
	}

	defer file.Close()

	groups, persons, err = parseInput.ParseGroupsAndPersons(file)
	filename = filepath
	return
}

//handle save_as action
func handleSaveAs(filepath string) (err error) {
	file, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer file.Close()

	text, err := parseInput.FormatGroupsAndPersons(groups, persons)
	if err != nil {
		return err
	}
	_, err = file.WriteString(text)
	return err
}

//handle export as excel-file actions
func handleExport(filepath string, total bool) (err error) {
	file, err := parseInput.FormatGroupsAndPersonsToExcel(groups, persons, l, total)
	if err != nil {
		return err
	}
	return file.Save(filepath)
}

//update body with no changes
func updateBody() {
	form, err := url.ParseQuery(" ")
	if err != nil {
		log.Fatal(err)
	}

	body := handleChanges(form, "")

	// Send message to webserver
	sendBody(body)
}

func sendBody(body string) {
	w.Send(Message{
		"body",
		body,
	})
}

// main function
func main() {
	initLangs()
	oslang := os.Getenv("LANG")
	l = langs["en"] // fallback
	for lang := range langs {
		if strings.HasPrefix(oslang, lang) {
			l = langs[lang]
		}
	}

	// properly exit on receiving exit signal
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		exit()
	}()

	// parse project opened with the program or restore from autosafe
	var importpath string
	if len(os.Args) > 1 {
		importpath = os.Args[1]
	} else {
		restoreFromAutosave()
	}

	// setup http listeners
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(&assetfs.AssetFS{
		Asset:     Asset,
		AssetDir:  AssetDir,
		AssetInfo: AssetInfo,
		Prefix:    "static",
	})))
	http.HandleFunc("/", handleRoot)

	// listen on random free port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(listener.Addr())

	go func() {
		log.Fatal(http.Serve(listener, nil))
	}()
	time.Sleep(time.Millisecond * 10)
	// Initialize astilectron
	var a, _ = astilectron.New(astilectron.Options{
		AppName: "Groupt Matcher",
		//AppIconDefaultPath: "<your .png icon>",
		//AppIconDarwinPath:  "<your .icns icon>",
		BaseDirectoryPath: "cache",
	})
	defer a.Close()

	err = a.Start()
	if err != nil {
		log.Fatal(err)
	}

	urlString := "http://" + listener.Addr().String()
	if importpath != "" {
		urlString += "/?import=" + url.QueryEscape(importpath)
	}

	// Create a new window
	w, err = a.NewWindow(urlString, &astilectron.WindowOptions{
		Center: astilectron.PtrBool(true),
		Height: astilectron.PtrInt(800),
		Width:  astilectron.PtrInt(1200),
	})
	if err != nil {
		log.Fatal(err)
	}

	err = w.Create()
	if err != nil {
		log.Fatal(err)
	}

	var m *astilectron.Menu
	var createMenu func()
	createMenu = func() {
		m = w.NewMenu([]*astilectron.MenuItemOptions{
			{
				Label: astilectron.PtrStr(l["file"]),
				SubMenu: []*astilectron.MenuItemOptions{
					{Label: astilectron.PtrStr(l["open"]), OnClick: func(e astilectron.Event) bool {
						w.Send(struct {
							Cmd string
						}{"openFile"})
						return false
					}},
					{Label: astilectron.PtrStr(l["save"]), OnClick: func(e astilectron.Event) bool {

						if projectPath != "" {
							err := handleSaveAs(projectPath)
							if err == nil {
								return false
							}
						}

						w.Send(struct {
							Cmd string
						}{"save_as"})

						return false
					}},
					{Label: astilectron.PtrStr(l["save_as"]), OnClick: func(e astilectron.Event) bool {
						w.Send(struct {
							Cmd string
						}{"save_as"})
						return false
					}},
					{Label: astilectron.PtrStr(l["export"]), SubMenu: []*astilectron.MenuItemOptions{
						{Label: astilectron.PtrStr(l["exlimited"]), OnClick: func(e astilectron.Event) bool {
							w.Send(struct {
								Cmd string
							}{"export_limited"})
							return false
						}},
						{Label: astilectron.PtrStr(l["extotal"]), OnClick: func(e astilectron.Event) bool {
							w.Send(struct {
								Cmd string
							}{"export_total"})
							return false
						}},
					}},
					{Label: astilectron.PtrStr(l["exit"]), Role: astilectron.MenuItemRoleQuit},
				},
			},
			{
				Label: astilectron.PtrStr(l["language"]),
				SubMenu: func() []*astilectron.MenuItemOptions {
					o := make([]*astilectron.MenuItemOptions, 0, len(langs))
					for n, lang := range langs {
						name := n
						o = append(o, &astilectron.MenuItemOptions{
							Label:   astilectron.PtrStr(lang["#name"]),
							Type:    astilectron.MenuItemTypeRadio,
							Checked: astilectron.PtrBool(lang["#name"] == l["#name"]),
							OnClick: func(e astilectron.Event) bool {
								l = langs[name]
								go func() {
									m.Destroy()
									createMenu()
									updateBody()
								}()
								return false
							},
						})
					}
					sort.Slice(o, func(i, j int) bool {
						return strings.Compare(*o[i].Label, *o[j].Label) < 0
					})
					return o
				}(),
			},
			{
				Label: astilectron.PtrStr(l["help"]),
				SubMenu: []*astilectron.MenuItemOptions{
					{Label: astilectron.PtrStr(l["help"]), Role: astilectron.MenuItemRoleHelp}, // TODO: open documentation
					{Label: astilectron.PtrStr(l["about"]), Role: astilectron.MenuItemRoleAbout, OnClick: func(e astilectron.Event) bool {
						go func() {
							aboutWindow, err := a.NewWindow(urlString+"/?about", &astilectron.WindowOptions{
								Center:    astilectron.PtrBool(true),
								Width:     astilectron.PtrInt(900),
								Height:    astilectron.PtrInt(450),
								Resizable: astilectron.PtrBool(false),
							})
							if err != nil {
								log.Fatal(err)
							}
							err = aboutWindow.Create()
							if err != nil {
								log.Fatal(err)
							}
						}()
						return false
					}},
				},
			},
		})
		m.Create()
	}
	createMenu()

	w.OpenDevTools()

	// Listen to messages sent by webserver
	w.On(astilectron.EventNameWindowEventMessage, func(e astilectron.Event) (deleteListener bool) {
		var msg string
		err := e.Message.Unmarshal(&msg)
		if err != nil {
			log.Println(err)
		}

		msg = strings.Trim(strings.Trim(msg, "/"), "?")

		form, err := url.ParseQuery(msg)
		if err != nil {
			log.Fatal(err)
		}

		body := handleChanges(form, "")

		for _, message := range messages {
			w.Send(message)
		}

		// Send message to webserver
		sendBody(body)

		return
	})

	a.Wait()
	exit()
}

// randomly generate groups and persons for testing
func genGroupsAndPersons() ([]*matching.Group, []*matching.Person) {
	groups := make([]*matching.Group, 10)
	for i := 0; i < len(groups); i++ {
		groups[i] = matching.NewGroup(l["group"]+strconv.Itoa(i), 16, 12)
	}
	persons := make([]*matching.Person, 130)
	for i := 0; i < len(persons); i++ {
		prefs := make([]*matching.Group, 3)
		perms := rand.Perm(len(groups))
		for j := 0; j < 3; j++ {
			prefs[j] = groups[perms[j]]
		}
		persons[i] = matching.NewPerson(l["person"]+strconv.Itoa(i), prefs)
	}
	return groups, persons
}
