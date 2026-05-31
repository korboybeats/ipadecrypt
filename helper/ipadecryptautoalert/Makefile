THEOS ?= $(HOME)/theos
TARGET := iphone:clang:latest:15.0
ARCHS = arm64 arm64e
THEOS_PACKAGE_SCHEME ?= rootless

include $(THEOS)/makefiles/common.mk

TWEAK_NAME = ipadecryptautoalert

ipadecryptautoalert_FILES = Tweak.x
ipadecryptautoalert_CFLAGS = -fobjc-arc
ipadecryptautoalert_FRAMEWORKS = UIKit Foundation

include $(THEOS_MAKE_PATH)/tweak.mk
