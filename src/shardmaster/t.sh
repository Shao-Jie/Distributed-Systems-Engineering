#!/bin/sh
cnt=0
while true;
	do go test>out 2>&1||break;
	cnt=`expr $cnt + 1`
	echo $cnt>cntres
	echo $cnt
	echo --------------------------------------------------
done
