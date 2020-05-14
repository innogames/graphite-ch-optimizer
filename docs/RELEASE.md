# CREATE NEW RELEASE
When the code is ready to new release, create it with the tag `v${major_version}.${minor_version}.${patch_version}`.  
We have a [workflow](../.github/workflows/upload-assets.yml), which will upload created DEB and RPM packages together with hash sums as the release assets.

## Use Jenkins to upload packages to deb-drop repository
When the release is ready and assets are uploaded, launch the multibranch pipeline job configured against [Jenkinsfile](../Jenkinsfile) with desired version. It will download the package, compare hashsums and upload it to the repository.
