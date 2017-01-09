package matching

import(

)

type Combination struct {
    Quantity int
    Configuration []Part
}

type Part struct {
    Group *Group
    CandidateAmount int
}

//adds a configuration to all combinations it fitts to
func addToAnyIfFitting(config []*Group, c []Combination) bool {
    var wasAdded bool
    for i := range c {
        if c[i].addIfFitting(config) {
            wasAdded = true
        }
    }
    return wasAdded
}

//adds config to the combination c if it fitts
func (c *Combination) addIfFitting(config []*Group) bool {
    if c.isFitting(config) {
        //if config is a subconfiguration of c.Configuration (has less wishes, but the rest is equal) it is added,
        //but the function returns false, so that config is also added as a own combination
        c.Quantity++
        if len(config) == len(c.Configuration){
            return true
        }else {
            return false
        }
    }
    return false
}

//check if config is equal to c.Configuration (the order doesn't matter)
func (c *Combination) isFitting(config []*Group) bool {
    var count int
    for i := range c.Configuration {
        for j := range config {
            if c.Configuration[i].Group.Name == config[j].Name {
                count++
            }
        }
    }
    if count == len(config) {
        for i := range c.Configuration {
            for j := range config {
                if c.Configuration[i].Group.Name == config[j].Name {
                    c.Configuration[i].CandidateAmount++
                }
            }
        }
        return true
    }else {
        return false
    }
}
