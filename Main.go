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

	"github.com/asticode/go-astilectron"
	"github.com/tealeg/xlsx"
	"github.com/veecue/GroupMatcher/matching"
	"github.com/veecue/GroupMatcher/parseInput"
	"github.com/asticode/go-astilog"
	"sort"
)

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

var w *astilectron.Window

// scan language files from the locales directory and import them into the program
func initLangs() {
	langFiles, err := ioutil.ReadDir("locales")
	if err != nil {
		log.Fatal(err)
	}
	langs = make(map[string]map[string]string, len(langFiles))
	for _, langFile := range langFiles {
		filename := langFile.Name()
		if strings.HasSuffix(filename, ".json") {
			langname := strings.TrimSuffix(filename, ".json")
			lang := make(map[string]string)
			langData, err := ioutil.ReadFile("locales/" + langFile.Name())
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

	t, err := template.ParseFiles("templates/workspace.tmpl")
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

	// remove all persons from their groups
	if form["reset"] != nil {
		for i := range groups {
			groups[i].Members = make([]*matching.Person, 0)
		}
		notifications.WriteString(l["restored"] + "<br>")
	}

	var errorLine int
	errorLine = 0
	if form["import"] != nil {
		p := form.Get("import")
		if p != "undefined" { // user pressed cancel, do nothing
			err := handleImport(p)
			// display any error messages from import
			if err  == nil {
				notifications.WriteString(l["import_success"] + "<br>")
			} else {
				text, withLine, line := separateError(err.Error())
				errString := l["import_error"] + l[text]
				if withLine {
					errorLine = line
					errString = errString + l["line"] + strconv.Itoa(line)
				}
				groups = make([]*matching.Group, 0)
				persons = make([]*matching.Person, 0)
				errors.WriteString(errString)
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
				form["import"] = []string{err.Error()}
				editmodeContent = data
				editmode = true
			} else {
				form["import"] = []string{"success"}
			}
		} else {
			editmode = true
			editmodeContent, err = parseInput.FormatGroupsAndPersons(groups, persons)
			if err != nil {
				errors.WriteString(l[err.Error()] + "<br>")
			}
		}
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
	quote_value, _ := matching.NewMatcher(persons, groups).CalcQuote()

	// sort persons before display
	sortPersons()

	// create menu:
	if editmode {
		res.WriteString(`<div class="header"><ul><li><a onclick="astilectron.send('/')">` + l["return"] + `</a></li></ul></li></div>`)
	} else {
		res.WriteString(`<div class="header"><ul><li><a onclick="astilectron.send('/?clear')">` + l["reset"] + `</a></li><li><a onclick="astilectron.send('/?reset')">` + l["restore"] + `</a></li><li><a onclick="astilectron.send('/?match')">` + l["match_selected"] + `</a></li></ul></li></div>`)
	}

	// sidebar
	var quoteInDegree float64
	if quote_value == 0.0 {
		quoteInDegree = 0.0
	} else {
		quoteInDegree = (180 - (quote_value-1)*90)
	}

	res.WriteString(`<div class="sidebar">`)

	res.WriteString(`<scale style="background-image:linear-gradient(` + strconv.FormatFloat(quoteInDegree, 'f', 0, 64) + `deg, transparent 50%%, #2F3840 50%%),linear-gradient(0deg, #2F3840 50%%, transparent 50%%);"></scale>
		<div class="circle"><h1>` + strconv.FormatFloat(quote_value, 'f', 2, 64) + `</h1></div><div id="groups">` + l["group-overview"] + `<ul>`)
	if len(groups) == 0 {
		res.WriteString(`<li>` + l["none"] + `</li>`)
	} else {
		for i, group := range groups {
			htmlid := fmt.Sprint("g", i)
			if (len(group.Members) < group.MinSize || len(group.Members) > group.Capacity) && !matching.AllEmpty(groups) {
				res.WriteString(`<li><a style="color: #ca5773" href="#` + htmlid + `">` + group.Name + `</a></li>`)
			} else {
				res.WriteString(`<li><a href="#` + htmlid + `">` + group.Name + `</a></li>`)
			}
		}
	}

	fmt.Fprintf(&res, `</ul></div><div id="controls"><ul><li><a onclick="astilectron.send('?edit')">%s</a></li>`, l["edit"])

	res.WriteString(`</ul></div>`)

	res.WriteString(`</div>`)

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
								res.WriteString(`<td><a onclick="astilectron.send('/?person` + strconv.Itoa(personID) + `&delfrom=` + strconv.Itoa(i) + `#` + htmlid + `')" class="blue" title="` + l["rem_from_group"] + `">` + pref.StringWithSize() + `</a></td>`)
							} else {
								res.WriteString(`<td><a onclick="astilectron.send('/?person` + strconv.Itoa(personID) + `&delfrom=` + strconv.Itoa(i) + `&addto=` + strconv.Itoa(prefID) + `#` + htmlid + `')" title="` + l["add_to_group"] + `">` + pref.StringWithSize() + `</a></td>`)
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
		res.WriteString(`<textarea id="edit" name="data" class="lined">` + editmodeContent + `</textarea><script>$(function() {$(".lined").linedtextarea({selectedLine: ` + strconv.Itoa(errorLine) + `});});</script>`)
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

// generate exported file and serve it for download
func handleExport(res http.ResponseWriter, req *http.Request) {

	filetype := req.FormValue("type")
	var data string
	var file *xlsx.File
	var err error

	switch filetype {
	case "gm":
		filetype = "gm"
		data, err = parseInput.FormatGroupsAndPersons(groups, persons)
		if err != nil {
			return
		}
		res.Header().Add("content-disposition", "attachment; filename=GroupMatcher_export."+filetype)
		fmt.Fprint(res, data)
	case "extotal":
		filetype = "xlsx"
		file, err = parseInput.FormatGroupsAndPersonsToExcel(groups, persons, l, true)
		if err != nil {
			return
		}
		res.Header().Add("content-disposition", "attachment; filename=GroupMatcher_export."+filetype)
		err = file.Write(res)
		return
	case "exlimited":
		filetype = "xlsx"
		file, err = parseInput.FormatGroupsAndPersonsToExcel(groups, persons, l, false)
		if err != nil {
			return
		}
		res.Header().Add("content-disposition", "attachment; filename=GroupMatcher_export."+filetype)
		err = file.Write(res)
	default:
		return
	}
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
	w.Send(struct {
		Cmd string
		Body string
	}{
		"body",
		body,
	})
}

// main function
func main() {
	astilog.SetLogger(astilog.New(astilog.Configuration{
		AppName: "GroupMatcher",
		Verbose: true,
	}))
	astilog.Debug("Started")
	astilog.Error("Hi")

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
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))
	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/export", handleExport)

	// listen on random free port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		log.Fatal(err)
	}

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
					{Label: astilectron.PtrStr(l["save"])},
					{Label: astilectron.PtrStr(l["save_as"])},
					{Label: astilectron.PtrStr(l["export"]), SubMenu: []*astilectron.MenuItemOptions{
						{Label: astilectron.PtrStr(l["exlimited"])},
						{Label: astilectron.PtrStr(l["extotal"])},
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
						aboutWindow, err := a.NewWindow(urlString + "/?about", &astilectron.WindowOptions{
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

		body := handleChanges(form, "")

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