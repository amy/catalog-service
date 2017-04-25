package service

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fmt"

	"github.com/gorilla/mux"
	"github.com/rancher/catalog-service/model"
	"github.com/rancher/catalog-service/parse"
	"github.com/rancher/go-rancher/api"
)

func getTemplates(w http.ResponseWriter, r *http.Request, envId string) (int, error) {
	apiContext := api.GetApiContext(r)

	catalog := r.URL.Query().Get("catalogId")
	if catalog == "" {
		catalog = r.URL.Query().Get("catalog")
	}
	rancherVersion := r.URL.Query().Get("rancherVersion")

	// Backwards compatibility for older versions of CLI
	minRancherVersion := r.URL.Query().Get("minimumRancherVersion_lte")
	if rancherVersion == "" && minRancherVersion != "" {
		rancherVersion = minRancherVersion
	}

	templateBaseEq := r.URL.Query().Get("templateBase_eq")
	categories, _ := r.URL.Query()["category"]
	categoriesNe, _ := r.URL.Query()["category_ne"]

	// DB CALL: returns an array of templates
	templates := model.LookupTemplates(db, envId, catalog, templateBaseEq, categories, categoriesNe)

	resp := model.TemplateCollection{}

	startTotal := time.Now()

	//////////////////////////////////////////////////////////////////////

	startCatalogTime := time.Now()

	// create unique list of catalog IDs
	var catalogIDList map[string]bool
	catalogIDList = make(map[string]bool)

	for _, template := range templates {
		catalogIDList[strconv.Itoa(int(template.CatalogId))] = true
	}

	// join catalog IDs into string deliminated by commas
	var catalogIDs []string
	for key := range catalogIDList {
		catalogIDs = append(catalogIDs, key)
	}
	IDs := strings.Join(catalogIDs, ",")

	elapsedCatalogTime := time.Since(startCatalogTime)
	fmt.Printf("\n\n\nTIME FOR CATALOG IDs: %v\n\n\n", elapsedCatalogTime)

	fmt.Printf("CATALOG ID LIST: \n%v", IDs)

	// put it into one query
	var catalogModels []model.CatalogModel

	start := time.Now()

	db.Raw("SELECT * FROM catalog WHERE id IN ( ? );", IDs).Scan(&catalogModels)

	elapsed := time.Since(start)
	fmt.Printf("\n\n\n\nDB TOOK: %v \n\n\n\n\n", elapsed)

	// create map to lookup Catalogs based on ID
	startTemplateTime := time.Now()

	var collectedCatalogs map[uint]string
	collectedCatalogs = make(map[uint]string)
	for _, catalog := range catalogModels {
		collectedCatalogs[catalog.ID] = catalog.Name
	}

	numTemplates := 0
	for _, template := range templates {
		numTemplates++
		catalogName := collectedCatalogs[template.CatalogId]
		templateResource := templateResource(apiContext, catalogName, template, rancherVersion)
		if len(templateResource.VersionLinks) > 0 {
			resp.Data = append(resp.Data, *templateResource)
		}
	}

	elapsedTemplateTime := time.Since(startTemplateTime)
	//////////////////////////////////////////////////////////////////////

	elapsedTotal := time.Since(startTotal)
	fmt.Printf("\n\n\nTOTAL NUMBER OF TEMPLATES: %v\n\n\n", numTemplates)

	fmt.Printf("\n\n\nTIME FOR TEMPLATES: %v\n\n\n", elapsedTemplateTime)

	fmt.Printf("\n\n\nTOTAL TIME: %v \n\n\n", elapsedTotal)

	resp.Actions = map[string]string{
		"refresh": api.GetApiContext(r).UrlBuilder.ReferenceByIdLink("template", "") + "?action=refresh",
	}

	apiContext.Write(&resp)
	return 0, nil
}

func getTemplate(w http.ResponseWriter, r *http.Request, envId string) (int, error) {
	apiContext := api.GetApiContext(r)
	vars := mux.Vars(r)

	catalogTemplateVersion, ok := vars["catalog_template_version"]
	if !ok {
		return http.StatusBadRequest, errors.New("Missing paramater catalog_template_version")
	}

	rancherVersion := r.URL.Query().Get("rancherVersion")

	catalogName, templateName, templateBase, revisionOrVersion, _ := parse.TemplateURLPath(catalogTemplateVersion)

	template := model.LookupTemplate(db, envId, catalogName, templateName, templateBase)
	if template == nil {
		return http.StatusNotFound, errors.New("Template not found")
	}

	if revisionOrVersion == "" {
		if r.URL.RawQuery != "" && strings.EqualFold("image", r.URL.RawQuery) {
			icon, err := base64.StdEncoding.DecodeString(template.Icon)
			if err != nil {
				return http.StatusBadRequest, err
			}
			iconReader := bytes.NewReader(icon)
			http.ServeContent(w, r, template.IconFilename, time.Time{}, iconReader)
			return 0, nil
		} else if r.URL.RawQuery != "" && strings.EqualFold("readme", r.URL.RawQuery) {
			w.Write([]byte(template.Readme))
			return 0, nil
		}

		// Return template
		apiContext.Write(templateResource(apiContext, catalogName, *template, rancherVersion))
	} else {
		var version *model.Version
		revision, err := strconv.Atoi(revisionOrVersion)
		if err == nil {
			version = model.LookupVersionByRevision(db, envId, catalogName, templateBase, templateName, revision)
		} else {
			version = model.LookupVersionByVersion(db, envId, catalogName, templateBase, templateName, revisionOrVersion)
		}
		if version == nil {
			return http.StatusNotFound, errors.New("Version not found")
		}

		if r.URL.RawQuery != "" && strings.EqualFold("readme", r.URL.RawQuery) {
			w.Write([]byte(version.Readme))
			return 0, nil
		}

		versionResource, err := versionResource(apiContext, catalogName, *template, *version, rancherVersion)
		if err != nil {
			return http.StatusBadRequest, err
		}

		// Return template version
		apiContext.Write(versionResource)
	}

	return 0, nil
}

func refreshTemplates(w http.ResponseWriter, r *http.Request, envId string) (int, error) {
	if err := m.Refresh(envId, true); err != nil {
		return http.StatusBadRequest, err
	}
	if envId != "global" {
		if err := m.Refresh("global", true); err != nil {
			return http.StatusBadRequest, err
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return 0, nil
}
