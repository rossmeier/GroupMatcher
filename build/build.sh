#!/bin/bash

buildTarget() {
  export GOOS=$1
  export GOARCH=$2

  echo "handling $GOOS/$GOARCH"

  mkdir tmp

  #for f in $COPY_FILES
  #do
  #  echo "copying $f to output dir"
  #  cp -r ../$f tmp/
  #done

  if [ "$GOOS" == "windows" ]
  then
    cp ../GroupMatcherDE.lnk tmp/
    cp ../GroupMatcherEN.lnk tmp/
  fi

  cd tmp
    if [ "$GOOS" == "windows" ]
    then
        go build -ldflags -H=windowsgui github.com/veecue/GroupMatcher
        zip -r ../out/GroupMatcher-GreatMall-$GOOS-$GOARCH.zip *
    else
        go build github.com/veecue/GroupMatcher
        echo packing tar file...
        tar -czf ../out/GroupMatcher-GreatMall-$GOOS-$GOARCH.tar.gz *
    fi
  cd ..
  rm -rf tmp
}
rm -rf out
mkdir out

COPY_FILES="locales static"

OS="linux darwin windows"
ARCH="386 amd64"

for o in $OS
do
  for a in $ARCH
  do
    if [ "$o" == "darwin" ] && [ "$a" == "386" ]
    then
      continue
    fi
    buildTarget $o $a
  done
done
