package mergeableplease

import "fmt"

// Init creates the default config file and returns a report with the created path.
func (a *App) Init() (InitReport, error) {
	if a.initConfig == nil {
		return InitReport{}, errMissingDep("InitConfig")
	}

	path, err := a.initConfig()
	if err != nil {
		return InitReport{}, fmt.Errorf("could not initialize config: %w", err)
	}
	return InitReport{Path: path}, nil
}
