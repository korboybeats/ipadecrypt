#include "IDAppleAuthState.h"

IDAppleAuthState IDAppleAuthInitialState(void) {
    return IDAppleAuthStateChecking;
}

IDAppleAuthState IDAppleAuthStateForResult(IDAppleAuthCheckResult result) {
    switch (result) {
        case IDAppleAuthCheckResultSuccess:
            return IDAppleAuthStateVerified;
        case IDAppleAuthCheckResultAuthRequired:
            return IDAppleAuthStateRequired;
        case IDAppleAuthCheckResultInconclusive:
        case IDAppleAuthCheckResultNone:
            return IDAppleAuthStateUnknown;
    }
    return IDAppleAuthStateUnknown;
}

bool IDAppleAuthStateIsChecking(IDAppleAuthState state) {
    return state == IDAppleAuthStateChecking;
}

bool IDAppleAuthCompletionIsCurrent(unsigned long requestGeneration,
                                    unsigned long currentGeneration) {
    return requestGeneration == currentGeneration;
}

bool IDAppleAuthShouldRefreshSavedState(bool helperActive) {
    return !helperActive;
}
