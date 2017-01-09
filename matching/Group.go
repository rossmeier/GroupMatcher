package matching

import "fmt"

type Group struct {
	Members  []*Person
	MinSize  int
	Capacity int
	Name     string
}

func NewGroup(name string, capacity, minSize int) *Group {
	return &Group{Members: make([]*Person, 0), Name: name, Capacity: capacity, MinSize: minSize}
}

func (g *Group) String() string {
	return g.Name + ": " + fmt.Sprint(g.Members)
}

func (value *Group) IndexIn(slice []*Group) int {
	for p, v := range slice {
		if v == value {
			return p
		}
	}
	return -1
}

func (g *Group) StringWithSize() string {
	return fmt.Sprintf("%s (%d/%d-%d)", g.Name, len(g.Members), g.MinSize, g.Capacity)
}

func (g *Group) deletePerson(p *Person) {
	for i := 0; i < len(g.Members); i++ {
		if g.Members[i] == p {
			g.Members = append(g.Members[:i], g.Members[i+1:]...)
		}
	}
}

//adds a fitting person to the given group
func (g *Group) insertBestFrom(candidates []*Person, m *Matcher) {
	//searching with decreasing preference priority
	for i := 0;i < m.getMaxPref();i++ {
		//j := range candidates isn't possible because of changing slice length
		for j := len(candidates) - 1; j >= 0; j-- {
			if candidates[j].Preferences[i].Name == g.Name {
				//store candidate before deleting it
				candidate := &candidates[j]
				m.getHostGroup(candidates[j]).deletePerson(candidates[j])
				g.Members = append(g.Members, *candidate)
				return
			}
		}
	}
	return
}


func FindGroup(name string, groups []*Group) *Group {
	for i := 0; i < len(groups); i++ {
		if groups[i].Name == name {
			return groups[i]
		}
	}
	return nil
}

func PersonInNoGroup(person *Person, groups []*Group) bool {
	for _, g := range groups {
		for _, p := range g.Members {
			if p == person {
				return false
			}
		}
	}
	return true
}

func GetGrouplessPersons(persons []*Person, groups []*Group) []*Person {
	ret := make([]*Person, 0)
	for _, p := range persons {
		if PersonInNoGroup(p, groups) {
			ret = append(ret, p)
		}
	}
	return ret
}

func GetPersonsInGroup(persons []*Person, groups []*Group) []*Person {
	ret := make([]*Person, 0)
	for _, p := range persons {
		if !(PersonInNoGroup(p, groups)) {
			ret = append(ret, p)
		}
	}
	return ret
}

func AllEmpty(groups []*Group) bool {
	for i := range groups {
		if len(groups[i].Members) != 0 {
			return false
		}
	}
	return true
}
