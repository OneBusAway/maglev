echo "git_branch=$(git rev-parse --abbrev-ref HEAD)"
echo "git_build_time=$(date -u +"%d.%m.%Y @ %H:%M:%S UTC")" 
echo "git_commit_id=$(git rev-parse HEAD)"
echo "git_commit_id_abbrev=$(git rev-parse --short HEAD)"
echo "git_commit_message_full=$(git log -1 --pretty=%B | tr '\n' ' ')" # (tr '\n' ' ') is added to squash the commit message in one-line for further processing
echo "git_commit_message_short=$(git log -1 --oneline --pretty=%s)" # 
echo "git_commit_time=$(git log -1 --date=format:'%d.%m.%Y @ %H:%M:%S %Z' --format='%cd')" 
echo "git_commit_user_email=$(git log -n 1 --pretty=format:%ae)" 
echo "git_commit_user_name=$(git log -n 1 --pretty=format:%an)" 
echo "git_dirty=$(git diff --exit-code > /dev/null; echo $?)" 
echo "git_remote_origin_url=$(git config --get remote.origin.url)"
if git describe --tags >/dev/null 2>&1; then
    echo "git_build_version=$(git describe --tags --always)"
    echo "git_closest_tag_name=$(git describe --tags --abbrev=0)"  
    echo "git_closest_tag_commit_count=$(git rev-list $(git describe --tags --abbrev=0)..HEAD --count)" 
    echo "git_commit_id_describe=$(git describe --tags)" 
    echo "git_commit_id_describe_short=$(git describe --tags --abbrev=0)"  
    echo "git_tags=$(git tag -l --points-at HEAD | tr '\n' ',' | sed 's/,$//')"  
fi 

#TODO
#git.build.host
#git.build.time	
#git.build.user.email	
#git.build.user.name