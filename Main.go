// program to match persons with group preferences to their groups
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/asticode/go-astilectron"
	"github.com/elazarl/go-bindata-assetfs"
	"github.com/veecue/GroupMatcher/matching"
	"github.com/veecue/GroupMatcher/parseInput"
	"golang.org/x/text/language"
)

type Message struct {
	Cmd  string
	Body string
}

//go:generate go-bindata static/... locales templates
//go:generate rsrc -ico static/icon.ico -o FILE.syso
//go:generate go-astilectron-bindata

// map of all supported languages
var langs map[language.Tag]map[string]string

// current language
var l map[string]string

// set language per param
var langFlag = flag.String("lang", "", "The language of the UI")

// current project
var persons []*matching.Person
var groups []*matching.Group
var filename string

// buffer to save messages to be sent to astilectron
var messages []Message

var darktheme bool

// last path the project was saved to
var projectPath string

var w *astilectron.Window

// scan language files from the locales directory and import them into the program
func initLangs() {
	langFiles, err := AssetDir("locales")
	if err != nil {
		log.Fatal(err)
	}
	langs = make(map[language.Tag]map[string]string, len(langFiles))
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
			langs[language.MustParse(langname)] = lang
		}
	}

	tags := make([]language.Tag, 0, len(langs))
	for t := range langs {
		tags = append(tags, t)
	}
	langMatcher := language.NewMatcher(tags)

	userTags := make([]language.Tag, 0)
	if *langFlag != "" {
		userTags = append(userTags, language.Make(*langFlag))
	} else if os.Getenv("LANG") != "" {
		userTags = append(userTags, language.Make(os.Getenv("LANG")))
	} else {
		userTags = append(userTags, language.Make("en"))
	}

	tag, _, _ := langMatcher.Match(userTags...)
	l = langs[tag]
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
	if projectPath != "" {
		gm, err := parseInput.FormatGroupsAndPersons(groups, persons)
		if err != nil {
			log.Fatal(err)
		}

		ioutil.WriteFile(projectPath, []byte(gm), 0600)
	}
}

// autosafe and exit program
func exit() {
	autosafe()
	os.Exit(0)
}

// sorte the persons by alphabet for better UI
func sortPersons() {
	matching.Sort(persons)
	for _, g := range groups {
		matching.Sort(g.Members)
	}
}

func setDarkTheme(dark bool) {
	if darktheme == dark {
		return
	}
	darktheme = dark
	w.Send(struct {
		Cmd string
	}{
		"reload",
	})
}

//opens documentation in current language
func openDoc() {
	switch runtime.GOOS {
	case "linux":
		exec.Command("xdg-open", "documentation/"+l["#name"]+".pdf").Start()
	case "windows":
		exec.Command("cmd", "/c", "start", "documentation/"+l["#name"]+".pdf").Start()
	}
}

// http handler function for the about GUI
func handleAbout(res http.ResponseWriter, req *http.Request) {

	if req.URL.Query()["open"] != nil {
		url := req.URL.Query()["open"][0]
		switch runtime.GOOS { //open browser on localhost in different environments
		case "linux":
			exec.Command("xdg-open", url).Start()
		case "windows":
			exec.Command("cmd", "/c", "start", url).Start()
		}
	}

	tData, err := Asset("templates/about.tmpl")
	if err != nil {
		log.Fatal(err)
	}
	t, err := template.New("about").Parse(string(tData))
	if err != nil {
		log.Fatal(err)
	}

	body := bytes.Buffer{}
	body.WriteString(`<div class="about"><h1>GroupMatcher</h1><p>` + l["thanks"] + `</p><h2>` + l["about"] + `</h2><p>` + l["abouttext"] + `</p><h2>` + l["project"] + `</h2><p>` + l["github"] + ` - <a href="/about?open=https://github.com/veecue/GroupMatcher">` + l["visit"] + `</a></p><h2>` + l["license"] + `</h2><p>` + l["licensetext"] + `</p></div>`)

	var bodyHTML template.HTML

	bodyHTML = template.HTML(body.String())

	t.Execute(res, struct {
		Body  template.HTML
		Theme string
	}{
		bodyHTML,
		func() string {
			if darktheme {
				return "dark"
			} else {
				return "bright"
			}
		}(),
	})
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

	body := handleChanges(req.URL.Query(), req.PostFormValue("data"), true)

	var bodyHTML template.HTML

	bodyHTML = template.HTML(body)

	t.Execute(res, struct {
		Body  template.HTML
		Theme string
	}{
		bodyHTML,
		func() string {
			if darktheme {
				return "dark"
			} else {
				return "bright"
			}
		}(),
	})
}

//handle changes
func handleChanges(form url.Values, data string, calledByForm bool) string {
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
		notifications.WriteString(l["reseted"] + "<br>")
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

	if form["save"] != nil {
		notifications.WriteString(l["save_success"])
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
			groupStore, personStore, err := parseInput.ParseGroupsAndPersons(strings.NewReader(data))
			if err != nil {
				editmode = true
				importError = err.Error()
				editmodeContent = data
			} else {
				importError = "success"
				groups = groupStore
				persons = personStore

				// avoid loosing data on sudden exit with no path being provided
				if projectPath == "" {
					w.Send(struct {
						Cmd string
					}{"save_as"})
				}
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
		errors.WriteString(errString)
	}

	// clear if in invalid state or requested
	if (groups == nil || persons == nil) && errors.Len() == 0 {
		projectPath = ""
		groups = make([]*matching.Group, 0)
		persons = make([]*matching.Person, 0)
	}

	if form["clear"] != nil {
		projectPath = ""
		groups = make([]*matching.Group, 0)
		persons = make([]*matching.Person, 0)
		notifications.WriteString(l["cleared"] + "<br>")
	}

	// calculate matching quote for display
	quote_value, quoteInPercent := matching.NewMatcher(persons, groups).CalcQuote()

	// sort persons before display
	sortPersons()

	// create menu:
	if editmode {
		// provide hidden iframe for post forms to avoid reload via server handler
		res.WriteString(`<iframe name="form" style="display:none"></iframe>`)
		res.WriteString(`<form action="/?edit" method="POST" target="form">`)
		res.WriteString(`<div class="header"><div class="switch"><button type="submit">` + l["assign"] + `</button><a onclick="astilectron.send('?edit')">` + l["edit"] + `</a></div></div>`)
	} else {
		res.WriteString(`<div class="header"><ul><li><a onclick="astilectron.send('/?reset')">` + l["reset"] + `</a></li><li><a onclick="astilectron.send('/?match')">` + l["match_selected"] + `</a></li></ul><div class="switch"><a onclick="astilectron.send('/')">` + l["assign"] + `</a><a class="inactive" onclick="astilectron.send('?edit')">` + l["edit"] + `</a></div></div>`)
	}

	// sidebar
	res.WriteString(`<div class="sidebar">`)

	res.WriteString(`<div id="scale_container"><div id="scale" style="height: ` + strconv.FormatFloat(quoteInPercent, 'f', 2, 64) + `%;"><p>` + strconv.FormatFloat(quote_value, 'f', 2, 64) + `</p></div></div>`)

	for i, group := range groups {
		htmlid := fmt.Sprint("g", i)

		var disliked bool
		for _, m := range group.Members {
			for j := int(len(m.Preferences)/2) + 1; j < len(m.Preferences); j++ {
				if m.Preferences[j].Name == group.Name {
					disliked = true
				}
				break
			}
			if disliked {
				break
			}
		}

		if (len(group.Members) < group.MinSize || len(group.Members) > group.Capacity) && !matching.AllEmpty(groups) {
			res.WriteString(`<a class="unfitting group" href="#` + htmlid + `">` + group.StringWithSize() + `</a>`)
		} else if disliked {
			res.WriteString(`<a class="disliked group" href="#` + htmlid + `">` + group.StringWithSize() + `</a>`)
		} else {
			res.WriteString(`<a href="#` + htmlid + `" class="group">` + group.StringWithSize() + `</a>`)
		}
	}

	fmt.Fprintf(&res, `</div>`)

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
			res.WriteString(`<table class="left panel">`)
			res.WriteString(`<tr class="heading-big unassigned"><td colspan="5"><h3>` + l["unassigned"] + `</h3></td></tr>`)
			res.WriteString(`<tr class="headings-middle unassigned"><th><span class="spacer"></span></th><th>` + l["name"] + `</th><th>` + l["1stchoice"] + `</th><th>` + l["2ndchoice"] + `</th><th>` + l["3rdchoice"] + `</th></tr>`)
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
			res.Write([]byte(`</table>`))
		}

		// list group and their members
		if !editmode && len(groups) > 0 {
			res.WriteString("<table class=\"right panel\">")
			for i, group := range groups {
				htmlid := fmt.Sprint("g", i)
				res.Write([]byte(``))
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
			}
			res.WriteString("</table>")
		}
	}
	// display editmode panel with texbox
	if editmode {
		res.WriteString(`<div class="panel">`)
		res.WriteString(`<textarea id="edit" name="data" line="` + strconv.Itoa(errorLine) + `">` + editmodeContent + `</textarea>`)
		res.WriteString(`</div></form>`)
	}

	// end document
	res.WriteString("</div>")
	res.WriteString(`</div>`)

	// do refresh on real body (not hidden iframe)
	if calledByForm {
		sendBody(res.String())
		for _, message := range messages {
			w.Send(message)
		}
	}

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
	defer updateBody()

	file, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer file.Close()

	text, err := parseInput.FormatGroupsAndPersons(groups, persons)
	if err != nil {
		if err.Error() != "groups_empty" {
			return err
		}
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

	body := handleChanges(form, "", false)

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
	flag.Parse()

	initLangs()

	// properly exit on receiving exit signal
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		exit()
	}()

	// parse project opened with the program
	if flag.NArg() >= 1 {
		projectPath = flag.Arg(0)
	}

	// setup http listeners
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(&assetfs.AssetFS{
		Asset:     Asset,
		AssetDir:  AssetDir,
		AssetInfo: AssetInfo,
		Prefix:    "static",
	})))
	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/about", handleAbout)

	// listen on random free port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		log.Fatal(err)
	}

	// Build splasher
	var s *astisplash.Splasher
	if s, err = astisplash.New(); err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	// Splash
	var sp *astisplash.Splash
	if sp, err = s.Splash("static/loadscreen.png"); err != nil {
		log.Fatal(err)
	}

	go func() {
		log.Fatal(http.Serve(listener, nil))
	}()
	time.Sleep(time.Millisecond * 10)
	// Initialize astilectron
	var a, _ = astilectron.New(astilectron.Options{
		AppName:            "GroupMatcher",
		AppIconDefaultPath: "static/icon.png",
		AppIconDarwinPath:  "static/icon.ico",
		BaseDirectoryPath:  "cache",
	})
	defer a.Close()

	a.SetProvisioner(astilectron_bindata.NewProvisioner(Disembed))

	a.HandleSignals()

	err = a.Start()
	if err != nil {
		log.Fatal(err)
	}

	// Close splash
	if err = sp.Close(); err != nil {
		log.Fatal(err)
	}

	urlString := "http://" + listener.Addr().String()
	if projectPath != "" {
		urlString += "?import=" + projectPath
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
					{Label: astilectron.PtrStr(l["clear"]), OnClick: func(e astilectron.Event) bool {
						form, err := url.ParseQuery("clear")
						if err != nil {
							log.Fatal(err)
						}
						body := handleChanges(form, "", false)
						sendBody(body)
						return false
					}},
					{Label: astilectron.PtrStr(l["save"]), OnClick: func(e astilectron.Event) bool {

						if projectPath != "" {
							err := handleSaveAs(projectPath)
							if err == nil {
								form, err := url.ParseQuery("save")
								if err != nil {
									log.Fatal(err)
								}
								body := handleChanges(form, "", false)
								sendBody(body)
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

				Label: astilectron.PtrStr(l["theme"]),
				SubMenu: []*astilectron.MenuItemOptions{
					{Label: astilectron.PtrStr(l["bright"]), Type: astilectron.MenuItemTypeRadio, Checked: astilectron.PtrBool(!darktheme), OnClick: func(e astilectron.Event) bool {
						setDarkTheme(false)
						return false
					}},
					{Label: astilectron.PtrStr(l["dark"]), Type: astilectron.MenuItemTypeRadio, Checked: astilectron.PtrBool(darktheme), OnClick: func(e astilectron.Event) bool {
						setDarkTheme(true)
						return false
					}},
				},
			},
			{
				Label: astilectron.PtrStr(l["help"]),
				SubMenu: []*astilectron.MenuItemOptions{
					{Label: astilectron.PtrStr(l["help"]), Role: astilectron.MenuItemRoleHelp, OnClick: func(e astilectron.Event) bool {
						openDoc()
						return false
					}},
					{Label: astilectron.PtrStr(l["about"]), Role: astilectron.MenuItemRoleAbout, OnClick: func(e astilectron.Event) bool {
						go func() {
							aboutWindow, err := a.NewWindow(urlString+"/about", &astilectron.WindowOptions{
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

		body := handleChanges(form, "", false)

		// Send body to webserver
		sendBody(body)

		// send other messages
		for _, message := range messages {
			w.Send(message)
		}

		return
	})

	a.Wait()
	exit()
}
