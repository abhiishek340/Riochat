export const auth = {
  login: {
    title: "Sign in to {brand}",
    subtitle: "Welcome back. Enter your details to continue.",
    username: "Username or email",
    password: "Password",
    submit: "Sign in",
    submitting: "Signing in…",
    needsStrongerPrompt: "Enter your username and password.",
  },
  twoFactor: {
    title: "Verification code",
    subtitle: "Enter the code from your authenticator app.",
    code: "Code",
    submit: "Verify",
    cancel: "Use a different account",
  },
  callback: {
    working: "Finishing sign-in…",
  },
} as const;
