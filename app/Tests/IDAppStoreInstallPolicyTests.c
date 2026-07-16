#include "IDAppStoreInstallPolicy.h"

#include <assert.h>

int main(void) {
    assert(IDPostInstallOutcomeForState(false, false, false) == IDPostInstallOutcomeError);
    assert(IDPostInstallOutcomeForState(false, true, true) == IDPostInstallOutcomeError);
    assert(IDPostInstallOutcomeForState(true, false, false) == IDPostInstallOutcomeError);
    assert(IDPostInstallOutcomeForState(true, true, false) == IDPostInstallOutcomeComplete);
    assert(IDPostInstallOutcomeForState(true, true, true) == IDPostInstallOutcomeDecrypt);
    return 0;
}
