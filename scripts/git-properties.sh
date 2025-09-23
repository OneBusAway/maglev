git rev-parse --abbrev-ref HEAD #gets git.branch
hostname #gets git.build.host
date --rfc-3339=seconds # get git.build.time
git config user.email #gets git.build.user.email
git config user.name #gets git.build.user.name
echo "N/A" #Needs Tags gets git.build.version
echo "N/A" #Needs Tags gets git.closest.tag.commit.count
echo "N/A" #Needs Tags gets git.closest.tag.name
git rev-parse HEAD #get git.commit.id
git rev-parse --short HEAD #gets git.commit.id.abbrev
echo "N/A" #Needs Tags gets git.commit.id.describe
echo "N/A" #Needs Tags gets git.commit.id.describe-short
git log -1 --pretty=%B | tr '\n' ' ' # get git.commit.message.full (tr '\n' ' ') is added to squash the commit message in one-line for further processing
echo # adds a \n so that next command is on next line (for processing)
git log -1 --oneline --pretty=%s # gets git.commit.message.short (gets only first line of commit message)
git log -1 --date=format:"%d.%m.%Y @ %H:%M:%S %Z" --format="%cd" #get git.commit.time
git log -n 1 --pretty=format:%ae #gets git.commit.email
echo # adds a new line
git log -n 1 --pretty=format:%an #gets git.commit.name
echo # adds a new line
git diff --exit-code > /dev/null
echo $? #get git.dirty
git config --get remote.origin.url #gets git.remote.origin.url
echo "N/A" | tr '\n' ' ' #Needs Tags