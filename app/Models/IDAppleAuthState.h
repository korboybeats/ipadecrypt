#pragma once

#include <stdbool.h>

typedef enum {
    IDAppleAuthStateUnknown = 0,
    IDAppleAuthStateChecking,
    IDAppleAuthStateVerified,
    IDAppleAuthStateRequired,
} IDAppleAuthState;

typedef enum {
    IDAppleAuthCheckResultNone = 0,
    IDAppleAuthCheckResultInconclusive,
    IDAppleAuthCheckResultSuccess,
    IDAppleAuthCheckResultAuthRequired,
} IDAppleAuthCheckResult;

IDAppleAuthState IDAppleAuthInitialState(void);
IDAppleAuthState IDAppleAuthStateForResult(IDAppleAuthCheckResult result);
bool IDAppleAuthStateIsChecking(IDAppleAuthState state);
bool IDAppleAuthCompletionIsCurrent(unsigned long requestGeneration,
                                    unsigned long currentGeneration);
bool IDAppleAuthShouldRefreshSavedState(bool helperActive);
