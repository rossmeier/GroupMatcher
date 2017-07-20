// Import and export to/from the GroupMatcher (*.gm) format
package parseInput

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"github.com/tealeg/xlsx"
	"github.com/veecue/GroupMatcher/matching"
	"io"
	"strconv"
	"strings"
)

//Converts the current groups and persons (of package matcher) into a .xlsx document and saves into the project folder
func FormatGroupsAndPersonsToExcel(groups []*matching.Group, persons []*matching.Person, l map[string]string, printTotal bool) (*xlsx.File, error) {
	//create file
	file := xlsx.NewFile()
	sheet, err := file.AddSheet("GroupMatcherExport")
	if err != nil {
		fmt.Println(err)
		return nil, errors.New("export_error")
	}

	if printTotal {
		//create style for active preference
		activeStyle := xlsx.NewStyle()
		fill := *xlsx.NewFill("solid", "99999999", "99999999")
		activeStyle.Fill = fill
		activeStyle.ApplyFill = true

		//create group header
		sheet.AddRow()
		addCell(sheet, len(sheet.Rows)-1, l["group name"])
		addCell(sheet, len(sheet.Rows)-1, l["min_size"])
		addCell(sheet, len(sheet.Rows)-1, l["max_size"])
		addCell(sheet, len(sheet.Rows)-1, l["group_size"])

		//insert groups
		for i := range groups {
			sheet.AddRow()
			addCell(sheet, len(sheet.Rows)-1, groups[i].Name)
			addCell(sheet, len(sheet.Rows)-1, strconv.Itoa(groups[i].MinSize))
			addCell(sheet, len(sheet.Rows)-1, strconv.Itoa(groups[i].Capacity))
			addCell(sheet, len(sheet.Rows)-1, strconv.Itoa(len(groups[i].Members)))
		}

		//create persons header
		sheet.AddRow()
		sheet.AddRow()
		addCell(sheet, len(sheet.Rows)-1, l["person name"])
		addCell(sheet, len(sheet.Rows)-1, l["1stchoice"])
		addCell(sheet, len(sheet.Rows)-1, l["2ndchoice"])
		addCell(sheet, len(sheet.Rows)-1, l["3rdchoice"])

		//insert persons
		for i := range persons {
			sheet.AddRow()
			addCell(sheet, len(sheet.Rows)-1, persons[i].Name)
			assigned := persons[i].GetGroup(groups)
			for j := range persons[i].Preferences {
				addCell(sheet, len(sheet.Rows)-1, persons[i].Preferences[j].Name)
				//set different style for active preference
				if assigned != nil {
					if assigned.Name == persons[i].Preferences[j].Name {
						sheet.Rows[len(sheet.Rows)-1].Cells[len(sheet.Rows[len(sheet.Rows)-1].Cells)-1].SetStyle(activeStyle)
					}
				}
			}

		}
	} else {
		//create persons header
		sheet.AddRow()
		addCell(sheet, len(sheet.Rows)-1, l["person name"])
		addCell(sheet, len(sheet.Rows)-1, l["group_assigned"])

		//insert persons
		for i := range persons {
			sheet.AddRow()
			addCell(sheet, len(sheet.Rows)-1, persons[i].Name)
			assigned := persons[i].GetGroup(groups)
			if assigned != nil {
				addCell(sheet, len(sheet.Rows)-1, assigned.Name)
			}
		}
	}

	return file, nil
}

//Converts the current groups and persons (of package matcher) into a string in .gm syntax.
func FormatGroupsAndPersons(groups []*matching.Group, persons []*matching.Person) (string, error) {
	// buffer for efficient string concatenation
	var buf bytes.Buffer
	r := &buf
	if len(groups) == 0 {
		return "", errors.New("groups_empty")
	}
	if len(persons) == 0 {
		return "", errors.New("persons_empty")
	}

	// check whether all groups have the same parameters
	uniformMinMax := true
	min := groups[0].MinSize
	cap := groups[0].Capacity
	for _, g := range groups {
		if g.MinSize != min || g.Capacity != cap {
			uniformMinMax = false
		}
	}
	fmt.Fprint(r, "S")
	if uniformMinMax {
		fmt.Fprintf(r, ";%d;%d", groups[0].MinSize, groups[0].Capacity)
	}

	// print group definition
	fmt.Fprintln(r)

	// print grous
	for _, g := range groups {
		fmt.Fprint(r, g.Name)
		if !uniformMinMax {
			fmt.Fprintf(r, ";%d;%d", g.MinSize, g.Capacity)
		}
		fmt.Fprintln(r)
	}

	// print persons
	fmt.Fprintln(r, "P")
	for _, p := range persons {
		fmt.Fprint(r, p.Name)
		for _, pref := range p.Preferences {
			fmt.Fprint(r, ";"+pref.Name)
		}
		g := p.GetGroup(groups)
		if g != nil {
			fmt.Fprintln(r, "/"+g.Name)
		} else {
			fmt.Fprintln(r)
		}
	}
	return buf.String(), nil
}

//Converts the imported data into slices of groups and persons (package matcher).
func ParseGroupsAndPersons(data io.Reader) ([]*matching.Group, []*matching.Person, error) {
	//init return slices
	var groups []*matching.Group
	var persons []*matching.Person

	//convert data into bufio scanner
	scanner := bufio.NewScanner(data)

	//init processing variables (count, foundGroups and foundPersons are only needed for accurate error messages)
	emptyFile := true
	var count, mode, minSize, capacity int
	var foundGroups, foundPersons bool

	//do as long as there are lines
	for scanner.Scan() {
		//increase line count
		count++

		emptyFile = false

		//get line text and remove whitespaces
		text := scanner.Text()
		text = strings.TrimSpace(text)

		if len(text) != 0 {
			//if line contains person initializer set reading mode to 1, foundPersons to true and continue with next line
			if text == "P" {
				mode = 1
				foundPersons = true
				continue
			}
			//if line contains group initializer set reading mode to 2, set group parameters, check them for compatibility, and continue with next line
			if strings.HasPrefix(text, "S") && !foundGroups {
				mode = 2
				//var initializer string
				//initializer, minSize, capacity = parseGroupParams(text)
				_, minSize, capacity = parseGroupParams(text)

				if (minSize == -1 && capacity == -1) || minSize > capacity {
					errString := "syntax_error" + strconv.Itoa(count)
					return nil, nil, errors.New(errString)
				}
				foundGroups = true
				continue
			}
			switch mode {
			case 1:
				//parse person from line
				person, err := parsePerson(text, groups, persons)
				if err != nil {
					var errString string
					if !foundGroups {
						//in case groups were not declared before person initializer was found
						return nil, nil, errors.New("group_initializer_not_found")
					} else {
						//otherwise add line number to error message
						errString = err.Error() + strconv.Itoa(count)
					}
					return nil, nil, errors.New(errString)
				} else {
					//if no error occured add person to persons slice
					persons = append(persons, person)
				}
			case 2:
				//parse group form line
				group, err := parseGroup(text, minSize, capacity)
				if err != nil {
					//if error occured add line number to error message
					errString := err.Error() + strconv.Itoa(count)

					//if parsePerson() returns no error the person initializer was probably not found
					_, e := parsePerson(text, groups, persons)
					if e == nil {
						errString = "person_initializer_not_found"
					}
					return nil, nil, errors.New(errString)
				} else {
					//check for double use of a group name
					if matching.FindGroup(group.Name, groups) != nil {
						errString := "group_name_not_unique" + strconv.Itoa(count)
						return nil, nil, errors.New(errString)
					}
					//if no error occured add group to groups slice
					groups = append(groups, group)
				}

			}
		}
	}

	//if file was empty return appropriate error message
	if emptyFile {
		err := errors.New("empty_file")
		return nil, nil, err
	}

	//if persons initializer was not found return appropriate error message
	if !foundPersons {
		err := errors.New("person_initializer_not_found")
		return nil, nil, err
	}

	//if no error occured return groups and persons
	return groups, persons, nil
}

//Converts a single line (that should contain ether the group initializer or a group itself) into its parameters.
func parseGroupParams(str string) (string, int, int) {
	s := strings.Split(str, ";")
	if len(s) == 1 {
		return s[0], 0, 0
	}
	if len(s) != 3 {
		return "", -1, -1
	}
	min, err := strconv.Atoi(s[1])
	if err != nil {
		return "", -1, -1
	}
	cap, err := strconv.Atoi(s[2])
	if err != nil {
		return "", -1, -1
	}
	return s[0], min, cap
}

//Converts a single line (that should contain a person) into its parameters.
func parsePerson(str string, groups []*matching.Group, persons []*matching.Person) (*matching.Person, error) {
	params := strings.Split(str, ";")
	if len(params) < 2 {
		return nil, errors.New("missing_argument")
	}

	var assignTo *matching.Group
	lastIndex := len(params) - 1
	s := strings.Split(params[lastIndex], "/")
	if len(s) > 2 {
		return nil, errors.New("syntax_error")
	}
	if len(s) > 1 {
		params[lastIndex] = s[0]
		assignTo = matching.FindGroup(s[1], groups)
		if assignTo == nil {
			return nil, errors.New("group_not_found")
		}
	}

	for _, a := range params {
		if a == "" {
			return nil, errors.New("empty_argument")
		}
	}

	var prefs []*matching.Group
	for _, a := range params[1:] {
		prefs = append(prefs, matching.FindGroup(a, groups))
	}

	for _, g := range prefs {
		if g == nil {
			return nil, errors.New("group_not_found")
		}
	}

	p := matching.NewPerson(string(params[0]), prefs)

	if matching.FindPerson(p.Name, persons) != nil {
		return nil, errors.New("person_name_not_unique")
	}
	if assignTo != nil {
		assignTo.Members = append(assignTo.Members, p)
	}

	return p, nil
}

//Converts the parameters it gets from parseGroupParams() into a new group (package matcher) handling any errors.
func parseGroup(str string, minSize, capacity int) (*matching.Group, error) {
	name, min, cap := parseGroupParams(str)

	if min < 0 && cap < 0 {
		return nil, errors.New("syntax_error")
	}

	if min == 0 && cap == 0 {
		min = minSize
		cap = capacity
	}

	return matching.NewGroup(name, cap, min), nil
}

//adds a Cell to the given row of a .xlsx sheet
func addCell(sheet *xlsx.Sheet, row int, value string) {
	sheet.Rows[row].AddCell()
	sheet.Rows[row].Cells[len(sheet.Rows[row].Cells)-1].SetValue(value)
}
