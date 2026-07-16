#pragma once

#include <stdbool.h>

typedef enum {
    IDPostInstallOutcomeError = 0,
    IDPostInstallOutcomeComplete,
    IDPostInstallOutcomeDecrypt,
} IDPostInstallOutcome;

IDPostInstallOutcome IDPostInstallOutcomeForState(bool helperSucceeded,
                                                   bool installedPathPresent,
                                                   bool decryptAfterInstall);
