package main

import (
	"reflect"
	"testing"
)

func TestCommandArgsLeavesJFInvocationUnchanged(t *testing.T) {
	args := []string{"resolve", "gog"}
	actual, err := commandArgs("jf", args)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, args) {
		t.Fatalf("got %v, want %v", actual, args)
	}
}

func TestCommandArgsWrapsKnownCommand(t *testing.T) {
	actual, err := commandArgs("wrangler", []string{"r2", "bucket", "list"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "wrangler", "r2", "bucket", "list"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %v, want %v", actual, want)
	}
}

func TestCommandArgsMovesJFProfileBeforeCommand(t *testing.T) {
	actual, err := commandArgs("aws", []string{"--jf-profile", "aws-smarta-staging", "sts", "get-caller-identity"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "--profile", "aws-smarta-staging", "aws", "sts", "get-caller-identity"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("got %v, want %v", actual, want)
	}
}

func TestCommandArgsRejectsEmptyJFProfile(t *testing.T) {
	if _, err := commandArgs("aws", []string{"--jf-profile"}); err == nil {
		t.Fatal("expected an error")
	}
}
