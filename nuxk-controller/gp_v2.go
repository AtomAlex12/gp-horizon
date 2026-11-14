package main

import "regexp"

// The supervisor owns plugin updates and the controller owns GP credentials.
var gpV2Routes = map[string]*regexp.Regexp{
	"GET": regexp.MustCompile("^(?:" +
		"service/(status|releases|updates/[a-zA-Z0-9_-]+)|" +
		"search/(current|events)|backup/file|" +
		"data/(strategies|bundles)(/[a-zA-Z0-9_-]+(/archive)?)?|" +
		"data/runs(/[a-zA-Z0-9_-]+(/log)?)?|data/milestones|" +
		"data/domain-lists(/[0-9]+(/domains)?)?|" +
		"data/sources/[a-zA-Z0-9_-]+(/categories(/[a-zA-Z0-9_!-]+/domains)?|/update)?" +
		")$"),
	"POST": regexp.MustCompile("^(?:" +
		"search/stop|engines/(blockcheck2|blockcheck2-gp)/start|" +
		"engines/blockcheck2-gp/runs/[a-zA-Z0-9_-]+/resume|" +
		"data/(domain-lists|milestones)|data/sources/[a-zA-Z0-9_-]+/update|" +
		"backup(/restore(-file)?)?" +
		")$"),
	"PUT":    regexp.MustCompile("^data/domain-lists/[0-9]+$"),
	"DELETE": regexp.MustCompile("^(backup|data/runs/[a-zA-Z0-9_-]+|data/domain-lists/[0-9]+)$"),
}

func gpV2Allowed(method, path string) bool {
	route := gpV2Routes[method]
	return route != nil && route.MatchString(path)
}
