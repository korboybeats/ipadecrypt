#include "IDAppStoreInstallPolicy.h"

IDPostInstallOutcome IDPostInstallOutcomeForState(bool helperSucceeded,
                                                   bool installedPathPresent,
                                                   bool decryptAfterInstall) {
    if (!helperSucceeded || !installedPathPresent) {
        return IDPostInstallOutcomeError;
    }

    return decryptAfterInstall ? IDPostInstallOutcomeDecrypt
                               : IDPostInstallOutcomeComplete;
}
