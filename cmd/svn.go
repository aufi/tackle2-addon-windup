package main

import (
	"errors"
	"fmt"
	liberr "github.com/konveyor/controller/pkg/error"
	"github.com/konveyor/tackle2-hub/api"
	"io/ioutil"
	urllib "net/url"
	"os"
	pathlib "path"
	"strings"
)

//
// Subversion repository.
type Subversion struct {
	SCM
}

//
// Validate settings.
func (r *Subversion) Validate() (err error) {
	u, err := urllib.Parse(r.Application.Repository.URL)
	if err != nil {
		err = &SoftError{Reason: err.Error()}
		return
	}
	insecure, err := addon.Setting.Bool("svn.insecure.enabled")
	if err != nil {
		return
	}
	switch u.Scheme {
	case "http":
		if !insecure {
			err = &SoftError{
				Reason: "http URL used with snv.insecure.enabled = FALSE",
			}
			return
		}
	}
	return
}

//
// Fetch clones the repository.
func (r *Subversion) Fetch() (err error) {
	url := r.URL()
	_ = RmDir(SourceDir)
	addon.Activity("[SVN] Cloning: %s", url.String())
	id, found, err := addon.Application.FindIdentity(r.Application.ID, "source")
	if err != nil {
		return
	}
	if found {
		addon.Activity(
			"[SVN] Using credentials (%d) %s.",
			id.ID,
			id.Name)
	} else {
		id = &api.Identity{}
	}
	err = r.writeConfig()
	if err != nil {
		return
	}
	err = r.writePassword(id)
	if err != nil {
		return
	}
	err = r.WriteKey(id)
	if err != nil {
		return
	}
	insecure, err := addon.Setting.Bool("svn.insecure.enabled")
	if err != nil {
		return
	}
	cmd := Command{Path: "/usr/bin/svn"}
	cmd.Options.add("--non-interactive")
	if insecure {
		cmd.Options.add("--trust-server-cert")
	}
	cmd.Options.add("checkout", url.String(), SourceDir)
	err = cmd.Run()
	return
}

//
// URL returns the parsed URL.
func (r *Subversion) URL() (u *urllib.URL) {
	repository := r.Application.Repository
	u, _ = urllib.Parse(repository.URL)
	branch := r.Application.Repository.Branch
	if branch == "" {
		branch = "trunk"
	}
	u.Path += "/" + branch
	return
}

//
// writeConfig writes config file.
func (r *Subversion) writeConfig() (err error) {
	path := pathlib.Join(
		r.HomeDir,
		".subversion",
		"servers")
	_, err = os.Stat(path)
	if !errors.Is(err, os.ErrNotExist) {
		err = liberr.Wrap(os.ErrExist)
		return
	}
	err = r.EnsureDir(pathlib.Dir(path), 0755)
	if err != nil {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		err = liberr.Wrap(
			err,
			"path",
			path)
		return
	}
	proxy, err := r.proxy()
	if err != nil {
		return
	}
	_, err = f.Write([]byte(proxy))
	if err != nil {
		err = liberr.Wrap(
			err,
			"path",
			path)
	}
	_ = f.Close()
	return
}

//
// writePassword injects the password into: auth/svn.simple.
func (r *Subversion) writePassword(id *api.Identity) (err error) {
	if id.User == "" || id.Password == "" {
		return
	}
	cmd := Command{Path: "/usr/bin/svn"}
	cmd.Options.add("--non-interactive")
	cmd.Options.add("--username")
	cmd.Options.add(id.User)
	cmd.Options.add("--password")
	cmd.Options.add(id.Password)
	cmd.Options.add("info", r.URL().String())
	err = cmd.RunSilent()
	if err != nil {
		return
	}
	dir := pathlib.Join(
		r.HomeDir,
		".subversion",
		"auth",
		"svn.simple")
	files, err := os.ReadDir(dir)
	if err != nil {
		err = liberr.Wrap(
			err,
			"path",
			dir)
		return
	}
	path := pathlib.Join(dir, files[0].Name())
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		err = liberr.Wrap(
			err,
			"path",
			path)
		return
	}
	defer func() {
		_ = f.Close()
	}()
	content, err := ioutil.ReadAll(f)
	if err != nil {
		err = liberr.Wrap(
			err,
			"path",
			path)
		return
	}
	_, err = f.Seek(0, 0)
	if err != nil {
		err = liberr.Wrap(
			err,
			"path",
			path)
		return
	}
	s := "K 8\n"
	s += "passtype\n"
	s += "V 6\n"
	s += "simple\n"
	s += "K 8\n"
	s += "username\n"
	s += fmt.Sprintf("V %d\n", len(id.User))
	s += fmt.Sprintf("%s\n", id.User)
	s += "K 8\n"
	s += "password\n"
	s += fmt.Sprintf("V %d\n", len(id.Password))
	s += fmt.Sprintf("%s\n", id.Password)
	s += string(content)
	_, err = f.Write([]byte(s))
	if err != nil {
		err = liberr.Wrap(
			err,
			"path",
			path)
	}
	return
}

//
// proxy builds the proxy.
func (r *Subversion) proxy() (proxy string, err error) {
	kind := ""
	url := r.URL()
	switch url.Scheme {
	case "http":
		kind = "http"
	case "https",
		"git@github.com":
		kind = "https"
	default:
		return
	}
	p, err := addon.Proxy.Find(kind)
	if err != nil || p == nil || !p.Enabled {
		return
	}
	for _, h := range p.Excluded {
		if h == url.Host {
			return
		}
	}
	addon.Activity(
		"[SVN] Using proxy (%d) %s.",
		p.ID,
		p.Kind)
	var id *api.Identity
	if p.Identity != nil {
		id, err = addon.Identity.Get(p.Identity.ID)
		if err != nil {
			return
		}
	}
	proxy = "[global]\n"
	proxy += fmt.Sprintf("http-proxy-host = %s\n", p.Host)
	if p.Port > 0 {
		proxy += fmt.Sprintf("http-proxy-port = %d\n", p.Port)
	}
	if id != nil {
		proxy += fmt.Sprintf("http-proxy-username = %s\n", id.User)
		proxy += fmt.Sprintf("http-proxy-password = %s\n", id.Password)
	}
	proxy += fmt.Sprintf(
		"(http-proxy-exceptions = %s\n",
		strings.Join(p.Excluded, " "))
	return
}
