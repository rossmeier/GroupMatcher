#!/bin/bash

RELEASENAME=GreatMall

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
    rsrc -ico ../static/icon.ico -o ../FILE.syso
    cp ../GroupMatcherDE.exe tmp/
    cp ../GroupMatcherEN.exe tmp/
  fi

  cd tmp
    if [ "$GOOS" == "windows" ]
    then
        go build -ldflags -H=windowsgui github.com/veecue/GroupMatcher
        echo packing zip file...
        zip -r ../out/GroupMatcher-$RELEASENAME-$GOOS-$GOARCH.zip *
    else
        go build github.com/veecue/GroupMatcher
        echo packing tar file...
        tar -czf ../out/GroupMatcher-$RELEASENAME-$GOOS-$GOARCH.tar.gz *
    fi
  cd ..

  if [ "$GOOS" == "windows" ]
  then
    rm ../FILE.syso
  fi

  rm -rf tmp
}
rm -rf out
mkdir out

COPY_FILES="locales static"

#OS="linux darwin windows"
OS="windows"
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
