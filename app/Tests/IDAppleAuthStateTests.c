#include "IDAppleAuthState.h"

#include <assert.h>

int main(void) {
    assert(IDAppleAuthInitialState() == IDAppleAuthStateChecking);
    assert(IDAppleAuthStateForResult(IDAppleAuthCheckResultNone) == IDAppleAuthStateUnknown);
    assert(IDAppleAuthStateForResult(IDAppleAuthCheckResultSuccess) == IDAppleAuthStateVerified);
    assert(IDAppleAuthStateForResult(IDAppleAuthCheckResultAuthRequired) == IDAppleAuthStateRequired);
    assert(IDAppleAuthStateForResult(IDAppleAuthCheckResultInconclusive) == IDAppleAuthStateUnknown);

    assert(IDAppleAuthStateIsChecking(IDAppleAuthStateChecking));
    assert(!IDAppleAuthStateIsChecking(IDAppleAuthStateUnknown));
    assert(!IDAppleAuthStateIsChecking(IDAppleAuthStateVerified));
    assert(!IDAppleAuthStateIsChecking(IDAppleAuthStateRequired));

    assert(IDAppleAuthCompletionIsCurrent(4, 4));
    assert(!IDAppleAuthCompletionIsCurrent(3, 4));
    assert(IDAppleAuthShouldRefreshSavedState(false));
    assert(!IDAppleAuthShouldRefreshSavedState(true));
    return 0;
}
