THEOS ?= $(HOME)/theos
TARGET := iphone:clang:latest:15.0
ARCHS = arm64 arm64e
THEOS_PACKAGE_SCHEME ?= rootless
INSTALL_TARGET_PROCESSES = SpringBoard

include $(THEOS)/makefiles/common.mk

APPLICATION_NAME = ipadecrypt

ipadecrypt_FILES = main.m AppDelegate.m SceneDelegate.m Logging.m PrivateAPI.m IDJailbreakPaths.m \
                   Models/IDInstalledApp.m Models/IDSearchResult.m Models/IDAppStoreVersion.m \
                   Models/IDAppStoreInstallPolicy.c Models/IDAppleAuthState.c \
                   Services/IDAppEnumerator.m Services/IDITunesSearch.m \
                   Services/IDAutoalertArmer.m Services/IDStoreKitDownloader.m \
                   Services/IDHelperRunner.m Services/IDAppStoreHelperRunner.m \
                   Views/IDAppRowCell.m Views/IDHomeViewController.m \
                   Views/IDDecryptOptionsViewController.m \
                   Views/IDDecryptProgressViewController.m \
                   Views/IDAppStoreVersionPickerViewController.m
ipadecrypt_FRAMEWORKS = UIKit Foundation
ipadecrypt_CFLAGS = -fobjc-arc -Werror -IModels -IServices -IViews
ipadecrypt_RESOURCE_DIRS = Resources
ipadecrypt_CODESIGN_FLAGS = -Sentitlements.plist

ifeq ($(THEOS_PACKAGE_SCHEME),roothide)
ipadecrypt_LDFLAGS += $(THEOS_VENDOR_LIBRARY_PATH)/libroot.a -lroothide
endif

include $(THEOS_MAKE_PATH)/application.mk
