// data types and algorithms
package matching

import (
	"log"
	"sync"
	"errors"
	"time"
	"fmt"
)

type Matcher struct {
	Persons []*Person
	Groups  []*Group
}

func NewMatcher(persons []*Person, groups []*Group) *Matcher {
	return &Matcher{Groups: groups, Persons: persons}
}

// Simple helper function to try out many solutions and get the best
func (m *Matcher) MatchManyAndTakeBest(n int, hardTimeout time.Duration, softTimeout time.Duration) error {
	matchers, err := m.matchMany(n, hardTimeout, softTimeout)
	m.takeBest(matchers)
	return err
}

func (m *Matcher) SmartMatch() bool {

	// insert all persons into their wishes
	r := GetGrouplessPersons(m.Persons, m.Groups)
	for _, p := range r {
		worked := false
		for _, pref := range p.Preferences {
			if InsertPersonIntoFullGroup(p, pref) {
				worked = true
				break
			}
		}
		if !worked {
			return false
		}
	}

	return m.correct()
}

// insert person into a full group while kicking out others and still keeping the score as high as possible
func InsertPersonIntoFullGroup(p *Person, g *Group) bool {
	if len(g.Members) < g.Capacity {
		g.Members = append(g.Members, p)
		return true
	}
	var bestCandidate *Person
	var moveTo *Group
	var score int = -1000000
	// find person to move to another group
	for _, candidate := range g.Members {
		for i := g.IndexIn(candidate.Preferences) + 1; i != 0 && i < len(candidate.Preferences); i++ {
			if len(candidate.Preferences[i].Members) >= candidate.Preferences[i].Capacity {
				// Don't overfill groups
				continue
			}

			// score is calculated that empty groups are filled and people get their favorite wishes
			cScore := candidate.Preferences[i].MinSize - len(candidate.Preferences[i].Members) - i
			if cScore > score {
				bestCandidate = candidate
				moveTo = candidate.Preferences[i]
				score = cScore
			}
		}
	}
	// if there is no candidate that can be kicked out of his group, just return
	if bestCandidate == nil {
		return false
	}

	// move the found candidate to his new group and the given candidate into the group
	g.Members[bestCandidate.IndexIn(g.Members)] = p
	moveTo.Members = append(moveTo.Members, bestCandidate)
	return true
}

// asynchronously shuffle and match this matcher n times and return the new matchers
// if found any solution, stop calculation at softTimeout or keep going for at least one solution until hardTimeout
func (m *Matcher) matchMany(n int, hardTimeout time.Duration, softTimeout time.Duration) (matchers []*Matcher, err error) {
	j, err := ToJSON(m.Groups, m.Persons)
	if err != nil {
		log.Fatal(err)
		err = nil
	}
	var wg sync.WaitGroup
	wg.Add(n)
	ms := make([]*Matcher, n)
	start := time.Now()
	found := false

	defer func(){
		dur := time.Since(start)
		if dur > hardTimeout {
			err = errors.New("hardtimeout")
		}
		if dur > softTimeout && found {
			err = errors.New("softtimeout")
		}
	}()

	for i := range ms {
		go func(num int) {
			defer wg.Done()
			for {
				groups, persons, err := FromJSON(j)
				if err != nil {
					log.Fatal(err)
				}
				Shuffle(persons)
				m2 := NewMatcher(persons, groups)
				if m2.SmartMatch() {
					ms[num] = m2
					found = true
					return
				}
				dur := time.Since(start)
				if dur > hardTimeout {
					return
				}
				if dur > softTimeout && found {
					return
				}
			}
		}(i)
	}
	wg.Wait()
	matchers = make([]*Matcher,0)
	for _,m := range ms {
		if m != nil {
			matchers = append(matchers, m)
		}
	}
	return
}

// Use the matcher with the best result from the given slice for the current matcher
func (m *Matcher) takeBest(tries []*Matcher) {
	bestQuote := 0.0
	var bestTry *Matcher
	for _, try := range tries {
		_, q := try.CalcQuote()
		if q > bestQuote {
			bestQuote = q
			bestTry = try
		}
	}
	if bestTry != nil {
		copy(m.Groups, bestTry.Groups)
		copy(m.Persons, bestTry.Persons)
	}
}

//corrects the given matcher in matter of group length (SmartMatch doesn't take care of minSize)
func (m *Matcher) correct() bool {
	var flag bool
	var count int
	for count < 50 && !flag {
		count++
		flag = true
		for j := range m.Groups {
			if len(m.Groups[j].Members) < m.Groups[j].MinSize {
				amountNeeded := m.Groups[j].MinSize - len(m.Groups[j].Members)
				for k := 0;k < amountNeeded;k++ {
					freeC, nonFreeC := m.getCandidates(m.Groups[j])
					if len(freeC) != 0 {
						m.Groups[j].insertBestFrom(freeC, m)
					}else{
						m.Groups[j].insertBestFrom(nonFreeC, m)
						flag = false
					}
				}
			}
		}
	}
	return flag
}

//returns all candidates for a special group
func (m *Matcher) getCandidates(preference *Group) (freeC, neededC []*Person){
	var pref, group, member int
	for pref = 0; pref < m.getMaxPref(); pref++ {
		for group = range m.Groups {
			if m.Groups[group] != preference {
				for member = range m.Groups[group].Members {
					if len(m.Groups[group].Members[member].Preferences) > pref {
						if m.Groups[group].Members[member].Preferences[pref] == preference {
							if m.Groups[group].MinSize >= len(m.Groups[group].Members) {
								neededC = append(neededC, m.Groups[group].Members[member])
							}else{
								freeC = append(freeC, m.Groups[group].Members[member])
							}
						}
					}
				}
			}
		}
	}
	return
}

//checks the matcher for correctness in matter of total, but also group specific person amount
func (m *Matcher) CheckMatcher() (error, string) {
	var needComma bool = false
	var errString string

	//check for persons that are already assigned
	if m.numberAssigned() != 0 {
		return errors.New("assigned_persons"), ""
	}

	//check for group specific person amount
	// i := range m.Groups not possible because of changing Groups length
	for i := len(m.Groups) - 1; i >= 0; i-- {
		//if there are not enough candidates for one group
		if !enoughCandidates(m.Groups[i], m.Persons) {
			//create error message
			if needComma {
				errString = errString + ", " + m.Groups[i].Name
			} else {
				errString = errString + m.Groups[i].Name
				needComma = true
			}
			for j := range m.Persons {
				//delete equivalent preferences
				for k := len(m.Persons[j].Preferences) - 1; k >= 0; k-- {
					if m.Persons[j].Preferences[k] == m.Groups[i] {
						m.Persons[j].Preferences = append(m.Persons[j].Preferences[:k], m.Persons[j].Preferences[k+1:]...)
					}
				}
				//ceck for persons with no preferences left
				if len(m.Persons[j].Preferences) < 1 {
					return errors.New("person_no_pref"), errString
				}
			}
			//and finally delete group
			m.Groups = append(m.Groups[:i], m.Groups[i+1:]...)
		}
	}

	//check for basic combination problems
	//the algorithm doesn't detect an error if there are two combinations which necessaryly need space in one group 
	needComma = false
	var combinations []Combination
	//get all combinations and subcombinations
	m.sortByPrefLen()//sort persons by preference length, so that subconfigurations are also put into the main-configuration
	for i := range m.Persons {
		if !addToAnyIfFitting(m.Persons[i].Preferences, combinations) {
			var configuration []Part
			for j := range m.Persons[i].Preferences {
				configuration = append(configuration, Part{m.Persons[i].Preferences[j], 1})
			}
			combinations = append(combinations, Combination{1, configuration})
		}
	}

	var foundErr bool
	//check for overfilled combinations and create error message (more complicated combination errors are not caught)
	for i := range combinations {
		var totalCapacity int
		for j := range combinations[i].Configuration {
			//the capacity a group adds to the totalCapacity is limited by the larger one of Capacity or CandidateAmount
			if combinations[i].Configuration[j].Group.Capacity < combinations[i].Configuration[j].CandidateAmount {
				totalCapacity = totalCapacity + combinations[i].Configuration[j].Group.Capacity
			}else{
				totalCapacity = totalCapacity + combinations[i].Configuration[j].CandidateAmount
			}
		}
		if combinations[i].Quantity > totalCapacity { //check if there is enough space in combinations groups and create error message if necessary
			foundErr = true
			if needComma {
				errString = errString + ", " + combinations[i].Configuration[0].Group.Name
				for j := 1; j < len(combinations[i].Configuration); j++ {
					errString = errString + "|" + combinations[i].Configuration[j].Group.Name
				}
			} else {
				errString = errString + combinations[i].Configuration[0].Group.Name
				for j := 1; j < len(combinations[i].Configuration); j++ {
					errString = errString + "|" + combinations[i].Configuration[j].Group.Name
				}
				needComma = true
			}
		}
	}
	if foundErr {
		return errors.New("combination_overfilled"), errString
	}


	//ceck for total person amount
	var totalMin, totalCap int
	for i := range m.Groups {
		totalMin = totalMin + m.Groups[i].MinSize
		totalCap = totalCap + m.Groups[i].Capacity
	}
	if len(m.Persons) < totalMin || len(m.Persons) > totalCap {
		return errors.New("err_matching_too_few_many"), errString
	} else {
		if errString == "" {
			return nil, errString
		} else {
			return errors.New("group_deleted"), errString
		}
	}
}

//checks if there are enough persons with the right preference according to the current group
func enoughCandidates(group *Group, persons []*Person) bool {
	var count int
	for i := range persons {
		for j := range persons[i].Preferences {
			if persons[i].Preferences[j] == group {
				count++
			}
		}
	}
	if count >= group.MinSize {
		return true
	}
	return false
}

//return number of assigned persons
func (m *Matcher) numberAssigned() (n int) {
	for i := range m.Groups {
		n+=len(m.Groups[i].Members)
	}
	return
}

// calculate the average preference number every person got and the wish fulfilling quote in percent
func (m *Matcher) CalcQuote() (quote, percentage float64) {
	nQuote := 0
	nMaxQuote := 0
	nAssigned := 0
	for _, g := range m.Groups {
		for _, p := range g.Members {
			nQuote += g.IndexIn(p.Preferences)
			nMaxQuote += len(p.Preferences)
			nAssigned++
		}
	}
	if nMaxQuote == 0 {
		return 0,0
	}
	quote = 1 + float64(nQuote) / float64(nAssigned)
	percentage = 100 * (1 - float64(nQuote) / float64(nMaxQuote))
	return
}

//return host group of given persons
func (m *Matcher) getHostGroup(p *Person) *Group {
	for i := range m.Groups {
		for j := range m.Groups[i].Members {
			if m.Groups[i].Members[j] == p {
				return m.Groups[i]
			}
		}
	}
	return nil
}

//get maximum length of a persons Preferences
func (m *Matcher)getMaxPref() (max int) {
	for _,person := range m.Persons {
		if max < len(person.Preferences) {
			max = len(person.Preferences)
		}
	}
	return
}

//orders the persons from many to few wishes
func (m *Matcher) sortByPrefLen(){
	var persons []*Person
	currLen := m.getMaxPref()
	for currLen >= 0 {
		for i := range m.Persons{
			if currLen == len(m.Persons[i].Preferences){
				persons = append(persons, m.Persons[i])
			}
		}
		currLen--
	}
	m.Persons = persons
}

//print groups (testing purpose)
func (m *Matcher) printMatcher(){
	q, p := m.CalcQuote()

	fmt.Println()
	fmt.Println()
	fmt.Println("Quote:\t", q, "( ~ ", p, " %)")
	fmt.Println()

	for i := 0;i < len(m.Groups);i++{
		fmt.Println()
		fmt.Println("--------------------------------------------------------------------------------------")
		fmt.Println("Gruppenname: ",m.Groups[i].Name, "\t( ", len(m.Groups[i].Members) ," )")
		for j := 0; j < len(m.Groups[i].Members); j++ {
			fmt.Println(j,"\t:\t\t", m.Groups[i].Members[j].Name, "\t (", m.Groups[i].Members[j].Preferences[0].Name, ")\t(", m.Groups[i].Members[j].Preferences[1].Name, ")\t(", m.Groups[i].Members[j].Preferences[0].Name, ")")
		}
	}
}
