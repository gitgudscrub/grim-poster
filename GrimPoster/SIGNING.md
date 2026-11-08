# Preparing a signed release

The reported Windows 11 Home block is Smart App Control. A public source repository alone does not satisfy it. A trusted code-signing provider must sign the executable after building it. The old RSA update-manifest key is separate from Windows Authenticode signing.

1. Confirm the MIT licence and ownership of the Grim Poster artwork and source you intend to publish.
2. Create a public repository owned by you and upload this source tree, including the workflow. Exclude generated `dist`, ZIP, `.syso` and `runtime.js` files as listed in `.gitignore`.
3. Build using the workflow. Have a Windows test environment run a complete BOTC session, verify initial loading and reloads, capture/reveal/post behaviour, Game 2 prompts, and uninstallation. A currently blocking Smart App Control machine will need a trusted signed build to test this executable.
4. Apply at https://signpath.org/ for open-source signing. Provide the repository and release links, project description, maintainer identity and the documented WebView2 runtime-loading behaviour. Acceptance and the provider's security assessment are discretionary. Free signing cannot be promised.
5. If accepted, configure their GitHub integration and artifact rules, nominate maintainers/reviewers/approvers, enable MFA and adopt the exact required signing/privacy policy. Connect the approved build artifact to their signing process. Add provider attribution only after approval.
6. Verify the returned executable's Authenticode signature on Windows with `Get-AuthenticodeSignature .\\GrimPoster.exe`. Confirm the status is Valid and the expected publisher is shown. Timestamp the signature using the provider's supported workflow.
7. Publish the signed executable as a GitHub release asset. Distribute that file unchanged. Signing a ZIP alone does not sign the executable inside it.

A paid trusted signing provider is another route, independent of making the source public. Trusted signatures are relevant to Smart App Control, but signing does not override malware detection or guarantee acceptance under every Windows policy. SmartScreen reputation warnings can also persist for new signed releases.

References checked during preparation

- https://learn.microsoft.com/en-us/windows/apps/develop/smart-app-control/code-signing-for-smart-app-control
- https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation
- https://signpath.org/terms.html

No signing application has been submitted, no public repository has been created and no certificate or private signing key is included in this package.
