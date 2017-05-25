// program to match persons with group preferences to their groups
package main

//TODO: localize font

import (
	"github.com/veecue/GroupMatcher/matching"
	"github.com/veecue/GroupMatcher/parseInput"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
	"path"
	"os/signal"
	"github.com/tealeg/xlsx"
	"github.com/asticode/go-astilectron"
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

// open a browser so that the user is able to interact with the WebGUI
func startBrowser(url string) error {
	var err error
	switch runtime.GOOS { //open browser on localhost in different environments
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("cmd", "/c", "start", url).Start()
	}
	if err == nil {
		return nil
	} else {
		return errors.New("Could not open URL in browser")
	}
}

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

	var errors bytes.Buffer
	var notifications bytes.Buffer
	var err error

	// parse URL parameters
	form := req.URL.Query()

	// switch language
	if form["lang"] != nil {
		name := form.Get("lang")
		lang, ok := langs[name]
		if !ok {
			errors.WriteString(l["lang_not_found"] + ": " + name + "<br>")
		} else {
			l = lang
		}
	}

	//generate import mode
	importmode := false
	if form["importmode"] != nil {
		importmode = true
	}

	//generate export mode
	exportmode := false
	if form["exportmode"] != nil {
		exportmode = true
	}

	// remove all persons from their groups
	if form["reset"] != nil {
		for i := range groups {
			groups[i].Members = make([]*matching.Person, 0)
		}
		notifications.WriteString(l["restored"] + "<br>")
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
				http.Error(res, err.Error(), http.StatusInternalServerError)
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
			err = m.MatchManyAndTakeBest(50, time.Minute, 10 * time.Second)
			if err != nil {
				errors.WriteString(l[err.Error()] + "<br>")
			}
			groups, persons = m.Groups, m.Persons
		} else {
			if err.Error() == "combination_overfilled" {
				errors.WriteString(l["combination_overfilled"] + errGroups + "<br>")
			}else {
				errors.WriteString(l[err.Error()] + "<br>")
			}
		}
	}

	// delete the selected persons from the given groups
	if form["delfrom"] != nil {
		j, err := strconv.Atoi(form.Get("delfrom"))
		if err != nil {
			http.Error(res, err.Error(), http.StatusInternalServerError)
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
			http.Error(res, err.Error(), http.StatusInternalServerError)
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
		data := req.PostFormValue("data")
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

	// display any error messages from import
	var errorLine int
	errorLine = 0
	if form["import"] != nil {
		if form["import"][0] == "success" {
			notifications.WriteString(l["import_success"] + "<br>")
		} else {
			text, withLine, line := separateError(form["import"][0])
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

	// clear if in invalid state or requested
	if ((groups == nil || persons == nil) && errors.Len() == 0) || form["clear"] != nil {
		groups = make([]*matching.Group, 0)
		persons = make([]*matching.Person, 0)
		notifications.WriteString(l["cleared"] + "<br>")
	}

	var exitmode bool
	if form["exit"] != nil {
		exitmode = true
		notifications.WriteString(l["thanks"])
		errors.WriteString(l["close_now"])
	}

	// calculate matching quote for display
	quote_value, _ := matching.NewMatcher(persons, groups).CalcQuote()

	// sort persons before display
	sortPersons()

	// generate DOM
	fmt.Fprint(res, `<!DOCTYPE HTML><html><head><title>Group Matcher</title><meta charset="UTF-8"><style>td{padding:0 3px}</style>`)
	fmt.Fprint(res, `<link rel="stylesheet" href="static/style.css"><link rel="stylesheet" href="static/jquery-linedtextarea/jquery-linedtextarea.css">`)
	fmt.Fprint(res, `<script src="static/jquery-3.1.1.js"></script><script src="static/jquery-linedtextarea/jquery-linedtextarea.js"></script>`)

	fmt.Fprint(res, `</head><body>`)

	// create menu:
	if exportmode || importmode || editmode {
		fmt.Fprintf(res, `<div class="header"><ul><li><a href="/">%s</a></li></ul></li></div>`, l["return"])
	}else if exitmode {
		fmt.Fprintf(res, `<div class="header"><ul><li style="text-transform:none;">%s</li><li></ul></li></div>`, "&copy Justus Roßmeier, Christian Obermaier & Max Obermeier")
	}else{
		fmt.Fprintf(res, `<div class="header"><ul><li><a href="/?clear">%s</a></li><li><a href="/?reset">%s</a></li><li><a href="/?match">%s</a></li></ul></li></div>`, l["reset"], l["restore"], l["match_selected"])
	}

	//create exit button
	if !exitmode {
		fmt.Fprint(res, `<a href="/?exit"><div id="shutdown"></div></a>`)
	}

	// sidebar
	var quoteInDegree float64
	if quote_value == 0.0{
		quoteInDegree = 0.0
	} else {
		quoteInDegree = (180 - (quote_value-1) * 90)
	}
	fmt.Fprintf(res, `<div class="sidebar">`)
	if !exitmode {
		fmt.Fprintf(res, `<scale style="background-image:linear-gradient(%sdeg, transparent 50%%, #2F3840 50%%),linear-gradient(0deg, #2F3840 50%%, transparent 50%%);"></scale>
			<div class="circle"><h1>%s</h1></div><div id="groups">%s<ul>`, strconv.FormatFloat(quoteInDegree, 'f', 0, 64), strconv.FormatFloat(quote_value, 'f', 2, 64), l["group-overview"])
		if len(groups) == 0 {
			fmt.Fprintf(res, "<li>%s</li>", l["none"])
		} else {
			for i, group := range groups {
				htmlid := fmt.Sprint("g", i)
				if (len(group.Members) < group.MinSize || len(group.Members) > group.Capacity) && !matching.AllEmpty(groups) {
					fmt.Fprintf(res, `<li><a style="color: #ca5773" href="#%s">%s</a></li>`, htmlid, group.Name)
				}else{
					fmt.Fprintf(res, `<li><a href="#%s">%s</a></li>`, htmlid, group.Name)
				}
			}

		}
		if importmode {
			fmt.Fprintf(res, `</ul></div><div id="controls"><ul><li><a href="?edit">%s</a></li><li class ="active"><a href="?importmode">%s</a></li><li><a href="?exportmode">%s</a></li><li>`, l["edit"], l["import"], l["export"])
		}else if exportmode {
			fmt.Fprintf(res, `</ul></div><div id="controls"><ul><li><a href="?edit">%s</a></li><li><a href="?importmode">%s</a></li><li class ="active"><a href="?exportmode">%s</a></li><li>`, l["edit"], l["import"], l["export"])
		}else if editmode {
			fmt.Fprintf(res, `</ul></div><div id="controls"><ul><li class ="active"><a href="?edit">%s</a></li><li><a href="?importmode">%s</a></li><li><a href="?exportmode">%s</a></li><li>`, l["edit"], l["import"], l["export"])
		}else {
			fmt.Fprintf(res, `</ul></div><div id="controls"><ul><li><a href="?edit">%s</a></li><li><a href="?importmode">%s</a></li><li><a href="?exportmode">%s</a></li><li>`, l["edit"], l["import"], l["export"])
		}
		printSeperator := false
		for langname, lang := range langs {
			if printSeperator {
				fmt.Fprint(res, ` | `)
			}else{
				printSeperator = true
			}
			fmt.Fprintf(res, `<a href="?lang=%s">%s</a>`, langname, lang["#name"])
		}
		fmt.Fprint(res, `</li></ul></div>`)
	}else{
		fmt.Fprintf(res,`<div class="about">%s</div>`,l["about"])
	}
	fmt.Fprint(res,`</div>`)

	if !exitmode {
		fmt.Fprint(res, `<div id="content">`)
		// print notifications and errors:
		if errors.Len() > 0 {
			fmt.Fprintf(res, `<div class="errors">%s</div>`, errors.String())
		}
		if notifications.Len() > 0 {
			fmt.Fprintf(res, `<div class="notifications">%s</div>`, notifications.String())
		}
	}else{
		// print notifications and errors:
		if errors.Len() > 0 {
			fmt.Fprintf(res, `<div class="errors" style="animation:appear 5s;opacity:1;">%s</div>`, errors.String())
		}
		if notifications.Len() > 0 {
			fmt.Fprintf(res, `<div class="notifications">%s</div>`, notifications.String())
		}
	}


	if !exitmode {
		fmt.Fprint(res, `<div id="panels">`)
		// list unassigned persons:
		if !editmode && !importmode && !exportmode {

			grouplessPersons := matching.GetGrouplessPersons(persons, groups)
			if !editmode && len(grouplessPersons) > 0 {
				fmt.Fprint(res, `<table class="left panel"><form action="">`)
				fmt.Fprintf(res, `<tr class="heading-big unassigned"><td colspan="5"><h3>%s</h3></td></tr>`, l["unassigned"])
				fmt.Fprintf(res, `<tr class="headings-middle unassigned"><th><span class="spacer"></span></th><th>%s</th><th>%s</th><th>%s</th><th>%s</th></tr>`, l["name"], l["1stchoice"], l["2ndchoice"], l["3rdchoice"])
				//fmt.Fprintf(res, `<tr><td colspan="5"><button type="submit" name="match">%s</button></td></tr>`, l["match_selected"])
				for i, person := range grouplessPersons {
					fmt.Fprintf(res, `<tr class="person unassigned"><td><!--input type="checkbox" name="person%d"--></td><td>%s</td>`, i, person.Name)

					for i := 0; i < 3; i++ {
						if i >= len(person.Preferences) {
							fmt.Fprint(res, `<td>--------</td>`)
						} else {
							prefID := person.Preferences[i].IndexIn(groups)
							fmt.Fprintf(res, `<td><a href="?person%d&addto=%d" title="%s">%s</a></td>`, person.IndexIn(persons), prefID, l["add_to_group"], person.Preferences[i].StringWithSize())
						}
					}

					fmt.Fprint(res, "</tr>")
				}
				res.Write([]byte(`</form></table>`))
			}

			// list group and their members
			if !editmode && len(groups) > 0 {
				fmt.Fprint(res, "<table class=\"right panel\">")
				for i, group := range groups {
					htmlid := fmt.Sprint("g", i)
					res.Write([]byte(`<form action="">`))
					fmt.Fprintf(res, `<tr class="heading-big assigned"><td colspan="5"><h3 id="%s">%s</h3></td></tr>`, htmlid, group.StringWithSize())
					fmt.Fprintf(res, `<tr class="headings-middle assigned"><th><span class="spacer"></span></th><th>%s</th><th>%s</th><th>%s</th><th>%s</th></tr>`, l["name"], l["1stchoice"], l["2ndchoice"], l["3rdchoice"])
					for _, person := range group.Members {
						fmt.Fprintf(res, `<tr class="person assigned"><td><!--input type="checkbox" name="person%d"--></td><td>%s</td>`, i, person.Name)

						for j := 0; j < 3; j++ {
							if j >= len(person.Preferences) {
								fmt.Fprint(res, `<td>--------</td>`)
							} else {
								pref := person.Preferences[j]
								prefID := pref.IndexIn(groups)
								personID := person.IndexIn(persons)
								if pref == group {
									fmt.Fprintf(res, `<td><a href="/?person%d&delfrom=%d#%s" class="blue" title="%s">%s</a></td>`, personID, i, htmlid, l["rem_from_group"], pref.StringWithSize())
								} else {
									fmt.Fprintf(res, `<td><a href="/?person%d&delfrom=%d&addto=%d#%s" title="%s">%s</a></td>`, personID, i, prefID, htmlid, l["add_to_group"], pref.StringWithSize())
								}
							}
						}

						fmt.Fprint(res, "</tr>")
					}
					res.Write([]byte(`</form>`))
				}
				fmt.Fprint(res, "</table>")
			}
		}
	}else{
		fmt.Fprint(res, `<div id="content" style="width: calc(100% - 16em); height: calc(100vh - 3em - 2px); background-image:url(static/logo.png);background-repeat:no-repeat; background-size:80% auto; background-position:center center; opacity:0.8;"`)
	}


	// display editmode panel with texbox
	if editmode {
		fmt.Fprintf(res, `<div class="panel"><form action="/?edit" method="POST">`)
		fmt.Fprintf(res, `<textarea id="edit" name="data" class="lined">%s</textarea><script>$(function() {$(".lined").linedtextarea({selectedLine: %v});});</script>`, editmodeContent, errorLine)
		fmt.Fprintf(res, `<button type="submit">%s</button>`, l["save"])
		fmt.Fprint(res, `</form></div>`)
	}

	// create import panel
	if importmode {
		fmt.Fprint(res, `<div class="panel"><form enctype="multipart/form-data" action="/import" method="post">`)
		fmt.Fprintf(res, `<input type="file" name="uploadfile"><button type="submit">%s</button>`,l["upload"])
		fmt.Fprint(res, `</form></div>`)
	}

	// create export panel
	if exportmode {
		fmt.Fprint(res, `<div class="panel"><form action="/export" target="frame_export">`)
		fmt.Fprintf(res, `<select name="type"><option selected value="gm">GroupMatcher</option><option value="extotal">%s</option><option value="exlimited">%s</select> <button type="submit">%s</button>`, l["extotal"], l["exlimited"],l["export"])
		fmt.Fprint(res, `</form><iframe width="1" height="1" name="frame_export" style="distplay:none;"></iframe></div>`)
	}

	// end document
	fmt.Fprint(res, "</div>")
	fmt.Fprint(res, `</div></body></html>`)

	if exitmode {
		time.AfterFunc(time.Second, exit)
	}
}

// handle file-uploads for import
func handleImport(res http.ResponseWriter, req *http.Request) {
	var err error
	defer func() {
		if err == nil {
			res.Header().Add("Location", "/?import=success")
		} else {
			errString := "/?import=" + err.Error()
			res.Header().Add("Location", errString)
		}
		http.Error(res, "Redirect", 301)
	}()

	if req.Method == "GET" {
		return
	}

	err = req.ParseMultipartForm(1000000000)
	if err != nil {
		return
	}

	file, _, err := req.FormFile("uploadfile")
	if err != nil {
		return
	}

	defer file.Close()

	groups, persons, err = parseInput.ParseGroupsAndPersons(file)
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

// main function
func main() {
	initLangs()
	l = langs["de"]

	// properly exit on receiving exit signal
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		exit()
	}()

	// parse project opened with the program or restore from autosafe
	if len(os.Args) > 1 {
		file, err := os.OpenFile(os.Args[1], os.O_RDONLY, 0)
		if err != nil {
			log.Println("File not found: ", os.Args[1])
		}
		groups, persons, err = parseInput.ParseGroupsAndPersons(file)
		file.Close()
		if err != nil {
			text, withLine, line := separateError(err.Error())
			errString := l["import_error"] + l[text]
			if withLine {
				errString = errString + l["line"] + strconv.Itoa(line)
			}
			log.Println(errString)
			fmt.Scan()
			os.Exit(0)
		}
	} else {
		restoreFromAutosave()
	}

	// setup http listeners
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./static"))))
	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/import", handleImport)
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

	// Create a new window
	w, err := a.NewWindow("http://" + listener.Addr().String(), &astilectron.WindowOptions{
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

	m := w.NewMenu([]*astilectron.MenuItemOptions{
		{
			Label: astilectron.PtrStr("Datei"),
			SubMenu: []*astilectron.MenuItemOptions{
				{Label: astilectron.PtrStr("Öffnen")},
				{Label: astilectron.PtrStr("Speichern")},
				{Label: astilectron.PtrStr("Exportieren"), SubMenu: []*astilectron.MenuItemOptions{
					{Label: astilectron.PtrStr("Excel (teilweise)")},
					{Label: astilectron.PtrStr("Excel (vollständig)")},
				}},
				{Label: astilectron.PtrStr("Bearbeitungsmodus"), Type: astilectron.MenuItemTypeCheckbox},
				{Label: astilectron.PtrStr("Beenden"), Role: astilectron.MenuItemRoleQuit},
			},
		},
		{
			Label: astilectron.PtrStr("Verteilen"),
			SubMenu: []*astilectron.MenuItemOptions{
				{Label:astilectron.PtrStr("Löschen")},
				{Label:astilectron.PtrStr("Verteilen")},
				{Label:astilectron.PtrStr("Zurücksetzen")},
			},
		},
		{
			Label: astilectron.PtrStr("Sprache"),
			SubMenu: func() ([]*astilectron.MenuItemOptions) {
				o := make([]*astilectron.MenuItemOptions, 0, len(langs))
				for n, lang := range langs {
					name := n
					o = append(o, &astilectron.MenuItemOptions{
						Label: astilectron.PtrStr(lang["#name"]),
						Type: astilectron.MenuItemTypeRadio,
						OnClick: func(e astilectron.Event) bool {
							l = langs[name]
							return false
						},
					})
				}
				return o
			}(),
		},
	})
	m.Create()

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
