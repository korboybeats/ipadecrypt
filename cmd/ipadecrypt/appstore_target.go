package main

import (
	"fmt"

	"github.com/londek/ipadecrypt/internal/appstore"
)

type storeTarget struct {
	bundleId string
	appId    string
}

func parseStoreTargetArg(raw, commandName string) (storeTarget, error) {
	dt, err := parseDecryptArg(raw)
	if err != nil {
		return storeTarget{}, err
	}

	if dt.localPath != "" {
		return storeTarget{}, fmt.Errorf("%s: local IPA paths are not supported - pass a bundle-id, app-store-id, or app-store-url", commandName)
	}

	return storeTarget{bundleId: dt.bundleId, appId: dt.appId}, nil
}

func lookupStoreTargetApp(as *appstore.Client, acc *appstore.Account, target storeTarget) (appstore.App, error) {
	if target.appId != "" {
		return as.LookupByAppID(acc, target.appId)
	}

	return as.LookupByBundleID(acc, target.bundleId)
}
